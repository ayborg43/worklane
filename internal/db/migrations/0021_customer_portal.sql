-- A project's "client" — the contact whose customer portal sees this
-- project's invoices. Nullable: internal-only projects have no client.
ALTER TABLE projects ADD COLUMN contact_id BIGINT REFERENCES contacts(id) ON DELETE SET NULL;
CREATE INDEX idx_projects_contact_id ON projects(contact_id);

-- One-time, short-lived tokens emailed to a contact. Redeeming one (see
-- portal_sessions below) marks it used so it can't be replayed — the same
-- "credential vs. session" split auth already has between users and
-- sessions, just with an emailed link standing in for a password.
CREATE TABLE portal_magic_links (
    id BIGSERIAL PRIMARY KEY,
    contact_id BIGINT NOT NULL REFERENCES contacts(id) ON DELETE CASCADE,
    token_hash TEXT NOT NULL UNIQUE,
    expires_at TIMESTAMPTZ NOT NULL,
    used_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_portal_magic_links_contact_id ON portal_magic_links(contact_id);

-- Long-lived session established after redeeming a magic link — mirrors
-- the shape of "sessions" (id is the session token's SHA-256 hash) but is
-- entirely separate: a contact is not a user, and portal_session is a
-- different cookie from the main app's, so the two auth paths never mix.
CREATE TABLE portal_sessions (
    id TEXT PRIMARY KEY,
    contact_id BIGINT NOT NULL REFERENCES contacts(id) ON DELETE CASCADE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at TIMESTAMPTZ NOT NULL
);
CREATE INDEX idx_portal_sessions_contact_id ON portal_sessions(contact_id);
CREATE INDEX idx_portal_sessions_expires_at ON portal_sessions(expires_at);
