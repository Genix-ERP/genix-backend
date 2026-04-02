-- Seed default lead pipeline stages for every tenant + organization combination
-- These are the default stages users can then edit/reorder/delete as they wish

-- First clean up any partially-seeded lead stages from frontend attempts
DELETE FROM pipeline_stages WHERE pipeline_type = 'lead';

-- Insert default lead stages for each (tenant_id, organization_id) pair
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
WHERE o.is_active = true
ON CONFLICT DO NOTHING;
