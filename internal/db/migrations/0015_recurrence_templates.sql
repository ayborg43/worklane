-- NULL recurrence_unit means "not recurring". Set together as a group by
-- projects.Repo.SetRecurrence, never individually.
ALTER TABLE tasks ADD COLUMN recurrence_unit TEXT CHECK (recurrence_unit IN ('day', 'week', 'month'));
ALTER TABLE tasks ADD COLUMN recurrence_interval INT;
ALTER TABLE tasks ADD COLUMN recurrence_until DATE;

CREATE TABLE task_templates (
    id BIGSERIAL PRIMARY KEY,
    project_id BIGINT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    default_duration_days INT NOT NULL DEFAULT 1 CHECK (default_duration_days >= 1),
    default_assignee_id BIGINT REFERENCES users(id),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_task_templates_project_id ON task_templates(project_id);
