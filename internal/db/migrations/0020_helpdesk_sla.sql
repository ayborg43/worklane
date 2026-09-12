ALTER TABLE tickets ADD COLUMN first_responded_at TIMESTAMPTZ;

-- One row per priority, editable from the Helpdesk board (same "small,
-- user-configurable table" shape as crm_stages) rather than gated behind
-- admin settings — helpdesk already has no ACL of its own.
CREATE TABLE helpdesk_sla_policies (
    priority TEXT PRIMARY KEY CHECK (priority IN ('low', 'medium', 'high')),
    response_hours INT NOT NULL CHECK (response_hours > 0),
    resolution_hours INT NOT NULL CHECK (resolution_hours > 0),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

INSERT INTO helpdesk_sla_policies (priority, response_hours, resolution_hours) VALUES
    ('high', 2, 8),
    ('medium', 8, 48),
    ('low', 24, 120);

-- Backfill first_responded_at from each ticket's earliest comment so
-- existing tickets don't look artificially unanswered once SLA tracking
-- goes live.
UPDATE tickets t SET first_responded_at = sub.first_comment_at
FROM (SELECT ticket_id, MIN(created_at) AS first_comment_at FROM ticket_comments GROUP BY ticket_id) sub
WHERE sub.ticket_id = t.id;
