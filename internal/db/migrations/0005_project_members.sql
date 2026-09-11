CREATE TABLE project_members (
    project_id BIGINT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    added_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (project_id, user_id)
);
CREATE INDEX idx_project_members_user_id ON project_members(user_id);

-- Backfill so nothing already in use silently disappears: the owner is
-- always a member, plus anyone already assigned to a task or with a
-- timesheet entry logged against that project.
INSERT INTO project_members (project_id, user_id)
SELECT id, owner_id FROM projects
ON CONFLICT DO NOTHING;

INSERT INTO project_members (project_id, user_id)
SELECT DISTINCT t.project_id, t.assignee_id
FROM tasks t
WHERE t.assignee_id IS NOT NULL
ON CONFLICT DO NOTHING;

INSERT INTO project_members (project_id, user_id)
SELECT DISTINCT e.project_id, e.user_id
FROM timesheet_entries e
ON CONFLICT DO NOTHING;
