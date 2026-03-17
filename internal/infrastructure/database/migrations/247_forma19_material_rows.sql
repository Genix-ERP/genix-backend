-- Forma 19: Material consumption tracking columns on construction_act_line
ALTER TABLE construction_act_line
    ADD COLUMN IF NOT EXISTS row_type VARCHAR(20) DEFAULT 'base',
    ADD COLUMN IF NOT EXISTS boshi DECIMAL(18,4) DEFAULT 0,
    ADD COLUMN IF NOT EXISTS keldi DECIMAL(18,4) DEFAULT 0,
    ADD COLUMN IF NOT EXISTS sarf DECIMAL(18,4) DEFAULT 0,
    ADD COLUMN IF NOT EXISTS qoldi DECIMAL(18,4) DEFAULT 0,
    ADD COLUMN IF NOT EXISTS cost_price DECIMAL(18,4) DEFAULT 0,
    ADD COLUMN IF NOT EXISTS change_reason VARCHAR(100),
    ADD COLUMN IF NOT EXISTS change_note TEXT,
    ADD COLUMN IF NOT EXISTS approved_by BIGINT,
    ADD COLUMN IF NOT EXISTS approved_at TIMESTAMPTZ;

CREATE INDEX IF NOT EXISTS idx_act_line_row_type ON construction_act_line(row_type);
