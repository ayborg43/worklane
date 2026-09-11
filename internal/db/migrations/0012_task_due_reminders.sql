-- Tracks whether a task's due-date reminder has already been sent, so the
-- periodic reminder scan can fire it exactly once per due date regardless
-- of how often it runs.
ALTER TABLE tasks ADD COLUMN due_reminder_sent_at TIMESTAMPTZ;
