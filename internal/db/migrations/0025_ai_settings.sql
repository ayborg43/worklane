-- Singleton table (id pinned to 1) holding the admin-configured
-- OpenAI-compatible endpoint used for the "Rephrase" button on text
-- fields — same shape as mail_settings, for the same reason: a row
-- always exists after this migration, so reads never need NULL handling.
-- An empty base_url/api_key is what "not configured yet" looks like.
CREATE TABLE ai_settings (
    id SMALLINT PRIMARY KEY DEFAULT 1 CHECK (id = 1),
    base_url TEXT NOT NULL DEFAULT '',
    api_key TEXT NOT NULL DEFAULT '',
    model TEXT NOT NULL DEFAULT '',
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_by BIGINT REFERENCES users(id)
);
INSERT INTO ai_settings (id) VALUES (1);
