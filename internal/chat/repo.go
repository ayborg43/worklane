package chat

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrNotFound = errors.New("not found")

type Repo struct {
	pool *pgxpool.Pool
}

func NewRepo(pool *pgxpool.Pool) *Repo {
	return &Repo{pool: pool}
}

func dmKey(userA, userB int64) string {
	if userA > userB {
		userA, userB = userB, userA
	}
	return fmt.Sprintf("%d:%d", userA, userB)
}

// ListChannelsForUser returns every channel viewerID is a member of. For DM
// channels, OtherUserName is resolved to the other participant relative to
// viewerID (so the same channel row displays a different name per viewer).
func (r *Repo) ListChannelsForUser(ctx context.Context, viewerID int64) ([]Channel, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT c.id, c.name, c.kind, COALESCE(c.dm_key, ''), c.created_by, c.created_at,
		       COALESCE((
		         SELECT u.name FROM chat_channel_members m2
		         JOIN users u ON u.id = m2.user_id
		         WHERE m2.channel_id = c.id AND m2.user_id != $1
		         LIMIT 1
		       ), ''),
		       (SELECT COUNT(*) FROM chat_messages cm
		        WHERE cm.channel_id = c.id AND cm.id > COALESCE(m.last_read_message_id, 0))
		FROM chat_channels c
		JOIN chat_channel_members m ON m.channel_id = c.id
		WHERE m.user_id = $1
		ORDER BY c.kind, c.name`, viewerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Channel
	for rows.Next() {
		var c Channel
		if err := rows.Scan(&c.ID, &c.Name, &c.Kind, &c.DMKey, &c.CreatedBy, &c.CreatedAt, &c.OtherUserName, &c.UnreadCount); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// MarkRead advances the member's last_read_message_id to the channel's
// current newest message — messages that arrive over an already-open
// socket after this call won't be marked read until the page is next
// loaded (see chat.Handlers.renderChat).
func (r *Repo) MarkRead(ctx context.Context, channelID, userID int64) error {
	_, err := r.pool.Exec(ctx, `
		UPDATE chat_channel_members
		SET last_read_message_id = (SELECT MAX(id) FROM chat_messages WHERE channel_id = $1)
		WHERE channel_id = $1 AND user_id = $2`, channelID, userID)
	return err
}

func (r *Repo) TotalUnreadForUser(ctx context.Context, userID int64) (int, error) {
	var n int
	err := r.pool.QueryRow(ctx, `
		SELECT COUNT(*) FROM chat_messages cm
		JOIN chat_channel_members m ON m.channel_id = cm.channel_id
		WHERE m.user_id = $1 AND cm.id > COALESCE(m.last_read_message_id, 0)`, userID).Scan(&n)
	return n, err
}

func (r *Repo) GetChannel(ctx context.Context, id, viewerID int64) (*Channel, error) {
	var c Channel
	err := r.pool.QueryRow(ctx, `
		SELECT c.id, c.name, c.kind, COALESCE(c.dm_key, ''), c.created_by, c.created_at,
		       COALESCE((
		         SELECT u.name FROM chat_channel_members m2
		         JOIN users u ON u.id = m2.user_id
		         WHERE m2.channel_id = c.id AND m2.user_id != $2
		         LIMIT 1
		       ), '')
		FROM chat_channels c
		WHERE c.id = $1`, id, viewerID,
	).Scan(&c.ID, &c.Name, &c.Kind, &c.DMKey, &c.CreatedBy, &c.CreatedAt, &c.OtherUserName)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &c, nil
}

func (r *Repo) IsMember(ctx context.Context, channelID, userID int64) (bool, error) {
	var exists bool
	err := r.pool.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM chat_channel_members WHERE channel_id = $1 AND user_id = $2)`,
		channelID, userID,
	).Scan(&exists)
	return exists, err
}

// CreateChannel makes a public channel and adds every existing user as a
// member immediately — this app has no "browse/join public channels" UI, so
// channels are workspace-wide by default (mirrors the "all users see all
// projects" MVP scope cut).
func (r *Repo) CreateChannel(ctx context.Context, name string, createdBy int64) (*Channel, error) {
	var id int64
	err := r.pool.QueryRow(ctx,
		`INSERT INTO chat_channels (name, kind, created_by) VALUES ($1, 'channel', $2) RETURNING id`,
		name, createdBy,
	).Scan(&id)
	if err != nil {
		return nil, err
	}
	if _, err := r.pool.Exec(ctx, `
		INSERT INTO chat_channel_members (channel_id, user_id)
		SELECT $1, u.id FROM users u
		ON CONFLICT DO NOTHING`, id); err != nil {
		return nil, err
	}
	return r.GetChannel(ctx, id, createdBy)
}

func (r *Repo) FindOrCreateDM(ctx context.Context, userA, userB int64) (*Channel, error) {
	key := dmKey(userA, userB)

	var id int64
	err := r.pool.QueryRow(ctx, `SELECT id FROM chat_channels WHERE dm_key = $1`, key).Scan(&id)
	if err != nil {
		if !errors.Is(err, pgx.ErrNoRows) {
			return nil, err
		}
		err = r.pool.QueryRow(ctx,
			`INSERT INTO chat_channels (name, kind, dm_key, created_by) VALUES ('', 'dm', $1, $2) RETURNING id`,
			key, userA,
		).Scan(&id)
		if err != nil {
			return nil, err
		}
		if _, err := r.pool.Exec(ctx,
			`INSERT INTO chat_channel_members (channel_id, user_id) VALUES ($1, $2), ($1, $3)`,
			id, userA, userB,
		); err != nil {
			return nil, err
		}
	}
	return r.GetChannel(ctx, id, userA)
}

const messageColumns = `m.id, m.channel_id, m.user_id, u.name, m.body,
	COALESCE(m.attachment_filename, ''), COALESCE(m.attachment_content_type, ''),
	COALESCE(m.attachment_size_bytes, 0), COALESCE(m.attachment_path, ''), m.created_at`

func scanMessage(row pgx.Row) (*Message, error) {
	var m Message
	err := row.Scan(&m.ID, &m.ChannelID, &m.UserID, &m.UserName, &m.Body,
		&m.AttachmentFilename, &m.AttachmentContentType, &m.AttachmentSizeBytes, &m.AttachmentPath, &m.CreatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &m, nil
}

// ListRecentMessages returns up to limit of the most recent messages in
// chronological order (oldest first) — the inner query picks the newest
// rows, the outer query re-sorts them for display.
func (r *Repo) ListRecentMessages(ctx context.Context, channelID int64, limit int) ([]Message, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT `+messageColumns+`
		FROM (
			SELECT * FROM chat_messages WHERE channel_id = $1 ORDER BY created_at DESC, id DESC LIMIT $2
		) m
		JOIN users u ON u.id = m.user_id
		ORDER BY m.created_at ASC, m.id ASC`, channelID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Message
	for rows.Next() {
		var m Message
		if err := rows.Scan(&m.ID, &m.ChannelID, &m.UserID, &m.UserName, &m.Body,
			&m.AttachmentFilename, &m.AttachmentContentType, &m.AttachmentSizeBytes, &m.AttachmentPath, &m.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func (r *Repo) GetMessage(ctx context.Context, id int64) (*Message, error) {
	return scanMessage(r.pool.QueryRow(ctx, `
		SELECT `+messageColumns+`
		FROM chat_messages m JOIN users u ON u.id = m.user_id
		WHERE m.id = $1`, id))
}

func (r *Repo) InsertMessage(ctx context.Context, channelID, userID int64, body string) (*Message, error) {
	var m Message
	err := r.pool.QueryRow(ctx, `
		INSERT INTO chat_messages (channel_id, user_id, body) VALUES ($1, $2, $3)
		RETURNING id, channel_id, user_id, body, created_at`,
		channelID, userID, body,
	).Scan(&m.ID, &m.ChannelID, &m.UserID, &m.Body, &m.CreatedAt)
	if err != nil {
		return nil, err
	}
	return &m, nil
}

// InsertAttachmentMessage records a message that carries a file — body may
// be empty (a bare file share with no caption).
func (r *Repo) InsertAttachmentMessage(ctx context.Context, channelID, userID int64, body, filename, contentType string, size int64, path string) (*Message, error) {
	var m Message
	err := r.pool.QueryRow(ctx, `
		INSERT INTO chat_messages (channel_id, user_id, body, attachment_filename, attachment_content_type, attachment_size_bytes, attachment_path)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		RETURNING id, channel_id, user_id, body, created_at`,
		channelID, userID, body, filename, contentType, size, path,
	).Scan(&m.ID, &m.ChannelID, &m.UserID, &m.Body, &m.CreatedAt)
	if err != nil {
		return nil, err
	}
	m.AttachmentFilename = filename
	m.AttachmentContentType = contentType
	m.AttachmentSizeBytes = size
	m.AttachmentPath = path
	return &m, nil
}
