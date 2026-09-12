ALTER TABLE projects ADD COLUMN billable_rate NUMERIC(10, 2) NOT NULL DEFAULT 0;
ALTER TABLE timesheet_entries ADD COLUMN billable BOOLEAN NOT NULL DEFAULT true;

CREATE TABLE invoices (
    id BIGSERIAL PRIMARY KEY,
    project_id BIGINT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    period_start DATE NOT NULL,
    period_end DATE NOT NULL,
    status TEXT NOT NULL DEFAULT 'draft' CHECK (status IN ('draft', 'sent', 'paid')),
    total_amount NUMERIC(12, 2) NOT NULL DEFAULT 0,
    created_by BIGINT NOT NULL REFERENCES users(id),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_invoices_project_id ON invoices(project_id);

-- Line items snapshot description/hours/rate/amount at generation time
-- rather than referencing timesheet_entries live — an invoice is a
-- historical financial record and must not silently change if the
-- underlying entry is later edited or deleted.
CREATE TABLE invoice_lines (
    id BIGSERIAL PRIMARY KEY,
    invoice_id BIGINT NOT NULL REFERENCES invoices(id) ON DELETE CASCADE,
    work_date DATE NOT NULL,
    user_name TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    hours NUMERIC(6, 2) NOT NULL,
    rate NUMERIC(10, 2) NOT NULL,
    amount NUMERIC(12, 2) NOT NULL
);
CREATE INDEX idx_invoice_lines_invoice_id ON invoice_lines(invoice_id);

-- Marks an entry as already billed so a second invoice over an overlapping
-- period can't double-charge the same hours.
ALTER TABLE timesheet_entries ADD COLUMN invoice_id BIGINT REFERENCES invoices(id);
ALTER TABLE timesheet_entries ADD COLUMN invoiced_at TIMESTAMPTZ;
CREATE INDEX idx_timesheet_entries_invoice_id ON timesheet_entries(invoice_id);
