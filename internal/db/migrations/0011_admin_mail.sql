ALTER TABLE users ADD COLUMN is_admin BOOLEAN NOT NULL DEFAULT false;

-- Bootstrap: the first registered user becomes admin so there is always
-- someone who can reach /settings once this migration has run.
UPDATE users SET is_admin = true WHERE id = (SELECT id FROM users ORDER BY id ASC LIMIT 1);

-- Singleton table (id is pinned to 1) holding the admin-configured outbound
-- mail settings. A row always exists after this migration so reads never
-- need NULL handling.
CREATE TABLE mail_settings (
    id SMALLINT PRIMARY KEY DEFAULT 1 CHECK (id = 1),
    smtp_host TEXT NOT NULL DEFAULT '',
    smtp_port INTEGER NOT NULL DEFAULT 587,
    smtp_username TEXT NOT NULL DEFAULT '',
    smtp_password TEXT NOT NULL DEFAULT '',
    from_address TEXT NOT NULL DEFAULT '',
    from_name TEXT NOT NULL DEFAULT '',
    use_tls BOOLEAN NOT NULL DEFAULT true,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_by BIGINT REFERENCES users(id)
);
INSERT INTO mail_settings (id) VALUES (1);
