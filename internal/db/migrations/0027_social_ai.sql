-- Per-target caption override, set when the compose form's AI "tailor per
-- platform" step produces (and the user keeps) a platform-specific caption
-- distinct from the post's shared body. Empty string (the default) means
-- "use the post's body as-is" for that target, same convention used
-- elsewhere in this schema for optional overrides.
ALTER TABLE social_post_targets ADD COLUMN caption_override TEXT NOT NULL DEFAULT '';
