-- Admin-configured OAuth app credentials, one row per supported platform —
-- same singleton-per-row shape as mail_settings, just keyed by platform
-- since there are several independent "apps" to configure instead of one.
CREATE TABLE social_app_credentials (
    platform TEXT PRIMARY KEY CHECK (platform IN ('x', 'linkedin', 'facebook', 'instagram', 'tiktok')),
    client_id TEXT NOT NULL DEFAULT '',
    client_secret TEXT NOT NULL DEFAULT '',
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_by BIGINT REFERENCES users(id)
);
INSERT INTO social_app_credentials (platform) VALUES ('x'), ('linkedin'), ('facebook'), ('instagram'), ('tiktok');

-- A connected company account for a platform, created once its OAuth
-- connect flow completes. One shared set of company accounts, not
-- per-user — matches how mail_settings is one shared config, not a
-- per-user mailbox.
CREATE TABLE social_accounts (
    id BIGSERIAL PRIMARY KEY,
    platform TEXT NOT NULL CHECK (platform IN ('x', 'linkedin', 'facebook', 'instagram', 'tiktok')),
    label TEXT NOT NULL DEFAULT '',
    external_account_id TEXT NOT NULL DEFAULT '',
    access_token TEXT NOT NULL DEFAULT '',
    refresh_token TEXT NOT NULL DEFAULT '',
    token_expires_at TIMESTAMPTZ,
    connected_by BIGINT NOT NULL REFERENCES users(id),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Short-lived, single-use state for the OAuth authorize -> callback round
-- trip — same "random token row with an expiry" shape as portal magic
-- links, just holding a PKCE code_verifier instead of a login identity.
CREATE TABLE social_oauth_states (
    state TEXT PRIMARY KEY,
    platform TEXT NOT NULL,
    code_verifier TEXT NOT NULL,
    initiated_by BIGINT NOT NULL REFERENCES users(id),
    expires_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE social_posts (
    id BIGSERIAL PRIMARY KEY,
    body TEXT NOT NULL,
    created_by BIGINT NOT NULL REFERENCES users(id),
    scheduled_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- One row per target account a post was sent to, so posting to three
-- connected accounts at once can have three independent outcomes (e.g. X
-- succeeds while LinkedIn's token has expired) instead of one shared
-- pass/fail per post.
CREATE TABLE social_post_targets (
    id BIGSERIAL PRIMARY KEY,
    post_id BIGINT NOT NULL REFERENCES social_posts(id) ON DELETE CASCADE,
    account_id BIGINT NOT NULL REFERENCES social_accounts(id) ON DELETE CASCADE,
    status TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'posted', 'failed')),
    remote_post_id TEXT NOT NULL DEFAULT '',
    error_message TEXT NOT NULL DEFAULT '',
    posted_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_social_post_targets_post_id ON social_post_targets(post_id);
CREATE INDEX idx_social_post_targets_pending ON social_post_targets(status) WHERE status = 'pending';
