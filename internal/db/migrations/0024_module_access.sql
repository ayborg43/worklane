-- Presence of a row means the user is BLOCKED from that module — absence
-- means allowed. This "blocklist" shape (rather than the more usual
-- allow-list join table) means introducing this table requires zero
-- backfill: every existing user keeps exactly the access they have today
-- until an admin explicitly restricts something.
CREATE TABLE user_module_restrictions (
    user_id    BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    module     TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, module)
);
