CREATE TABLE activities (
    id BIGSERIAL PRIMARY KEY,
    project_id BIGINT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    task_id BIGINT REFERENCES tasks(id) ON DELETE CASCADE,
    kind TEXT NOT NULL CHECK (kind IN ('call', 'meeting', 'todo', 'email')),
    note TEXT NOT NULL DEFAULT '',
    due_date DATE NOT NULL,
    assigned_to BIGINT NOT NULL REFERENCES users(id),
    created_by BIGINT NOT NULL REFERENCES users(id),
    done_at TIMESTAMPTZ,
    reminder_sent_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_activities_project_id ON activities(project_id);
CREATE INDEX idx_activities_task_id ON activities(task_id);
CREATE INDEX idx_activities_assigned_pending ON activities(assigned_to, done_at);
CREATE INDEX idx_activities_due_pending ON activities(due_date) WHERE done_at IS NULL;
