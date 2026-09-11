CREATE TABLE chat_channels (
    id BIGSERIAL PRIMARY KEY,
    name TEXT NOT NULL,
    kind TEXT NOT NULL DEFAULT 'channel' CHECK (kind IN ('channel', 'dm')),
    dm_key TEXT UNIQUE,
    created_by BIGINT REFERENCES users(id),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX idx_channels_name_channel ON chat_channels(name) WHERE kind = 'channel';

CREATE TABLE chat_channel_members (
    channel_id BIGINT NOT NULL REFERENCES chat_channels(id) ON DELETE CASCADE,
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    joined_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_read_message_id BIGINT,
    PRIMARY KEY (channel_id, user_id)
);
CREATE INDEX idx_channel_members_user_id ON chat_channel_members(user_id);

CREATE TABLE chat_messages (
    id BIGSERIAL PRIMARY KEY,
    channel_id BIGINT NOT NULL REFERENCES chat_channels(id) ON DELETE CASCADE,
    user_id BIGINT NOT NULL REFERENCES users(id),
    body TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_messages_channel_created ON chat_messages(channel_id, created_at);
