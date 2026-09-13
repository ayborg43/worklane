-- Optional media attachment for a post. Instagram and TikTok's publishing
-- APIs require a photo or video on every post — there's no text-only post
-- type like X has — so this is what makes those two platforms able to
-- publish at all, not just an enhancement to X's existing text-only flow.
ALTER TABLE social_posts ADD COLUMN media_path TEXT NOT NULL DEFAULT '';
ALTER TABLE social_posts ADD COLUMN media_content_type TEXT NOT NULL DEFAULT '';
