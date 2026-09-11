ALTER TABLE timesheet_entries
  ADD COLUMN status TEXT NOT NULL DEFAULT 'submitted' CHECK (status IN ('submitted', 'approved', 'rejected')),
  ADD COLUMN approved_by BIGINT REFERENCES users(id),
  ADD COLUMN approved_at TIMESTAMPTZ,
  ADD COLUMN rejection_reason TEXT NOT NULL DEFAULT '';
CREATE INDEX idx_timesheet_status ON timesheet_entries(status);
