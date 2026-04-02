-- Fix: unique index must include organization_id so different orgs can have same stage codes
-- Then seed default lead stages for ALL organizations that don't have them

-- Step 1: Drop the incomplete unique index and recreate with organization_id
DROP INDEX IF EXISTS idx_pipeline_stages_unique_code_type;

CREATE UNIQUE INDEX idx_pipeline_stages_unique_code_type_org
ON pipeline_stages (tenant_id, code, COALESCE(pipeline_type, 'opportunity'), COALESCE(organization_id, '00000000-0000-0000-0000-000000000000'));

-- Step 2: Insert default lead stages for every org that doesn't already have them
INSERT INTO pipeline_stages (id, tenant_id, name, code, sequence, probability, is_won, is_lost, color, is_active, pipeline_type, organization_id, created_at, updated_at)
SELECT
    uuid_generate_v4(),
    o.tenant_id,
    s.name,
    s.code,
    s.seq,
    s.prob,
    s.is_won,
    s.is_lost,
    s.color,
    true,
    'lead',
    o.id,
    NOW(),
    NOW()
FROM organizations o
CROSS JOIN (
    VALUES
        ('New',         'new',         0, 10.0,  false, false, 'blue'),
        ('Contacted',   'contacted',   1, 30.0,  false, false, 'amber'),
        ('In Progress', 'in_progress', 2, 50.0,  false, false, 'purple'),
        ('Qualified',   'qualified',   3, 80.0,  true,  false, 'green'),
        ('Lost',        'lost',        4, 0.0,   false, true,  'red')
) AS s(name, code, seq, prob, is_won, is_lost, color)
WHERE NOT EXISTS (
    SELECT 1 FROM pipeline_stages ps
    WHERE ps.tenant_id = o.tenant_id
      AND ps.organization_id = o.id
      AND ps.pipeline_type = 'lead'
      AND ps.code = s.code
);
