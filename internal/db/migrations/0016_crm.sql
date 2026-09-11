CREATE TABLE contacts (
    id BIGSERIAL PRIMARY KEY,
    name TEXT NOT NULL,
    company TEXT NOT NULL DEFAULT '',
    email TEXT NOT NULL DEFAULT '',
    phone TEXT NOT NULL DEFAULT '',
    created_by BIGINT NOT NULL REFERENCES users(id),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- is_won marks the single stage that counts as "deal closed successfully"
-- (used for the "Mark Won" shortcut and pipeline-value totals). "Lost" is
-- deliberately NOT a stage — it's the is_lost flag on the opportunity
-- itself, so a deal keeps its real stage history even after being marked
-- lost, matching Odoo's own lead/opportunity model.
CREATE TABLE crm_stages (
    id BIGSERIAL PRIMARY KEY,
    name TEXT NOT NULL,
    sort_order INT NOT NULL DEFAULT 0,
    is_won BOOLEAN NOT NULL DEFAULT false,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE opportunities (
    id BIGSERIAL PRIMARY KEY,
    name TEXT NOT NULL,
    contact_id BIGINT REFERENCES contacts(id) ON DELETE SET NULL,
    stage_id BIGINT NOT NULL REFERENCES crm_stages(id),
    owner_id BIGINT NOT NULL REFERENCES users(id),
    value_amount NUMERIC(12, 2) NOT NULL DEFAULT 0,
    expected_close_date DATE,
    is_lost BOOLEAN NOT NULL DEFAULT false,
    lost_reason TEXT NOT NULL DEFAULT '',
    created_by BIGINT NOT NULL REFERENCES users(id),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_opportunities_stage_id ON opportunities(stage_id);
CREATE INDEX idx_opportunities_contact_id ON opportunities(contact_id);
CREATE INDEX idx_opportunities_owner_id ON opportunities(owner_id);

-- Seed a sensible default pipeline so /crm isn't empty on first load.
INSERT INTO crm_stages (name, sort_order, is_won) VALUES
    ('New', 0, false),
    ('Qualified', 1, false),
    ('Proposal', 2, false),
    ('Negotiation', 3, false),
    ('Won', 4, true);
