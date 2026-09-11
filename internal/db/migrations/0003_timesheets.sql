CREATE TABLE timesheet_entries (
    id BIGSERIAL PRIMARY KEY,
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    project_id BIGINT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    task_id BIGINT REFERENCES tasks(id) ON DELETE SET NULL,
    work_date DATE NOT NULL,
    hours NUMERIC(5,2) NOT NULL CHECK (hours > 0 AND hours <= 24),
    description TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_timesheet_user_date ON timesheet_entries(user_id, work_date);
CREATE INDEX idx_timesheet_project_id ON timesheet_entries(project_id);
CREATE INDEX idx_timesheet_task_id ON timesheet_entries(task_id);
