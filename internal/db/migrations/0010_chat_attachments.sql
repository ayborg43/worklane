-- A chat message carries at most one attachment, so this is kept as nullable
-- columns directly on chat_messages rather than a separate join table (unlike
-- project/task attachments, which allow many files per task).
ALTER TABLE chat_messages
  ADD COLUMN attachment_filename TEXT,
  ADD COLUMN attachment_content_type TEXT,
  ADD COLUMN attachment_size_bytes BIGINT,
  ADD COLUMN attachment_path TEXT;
