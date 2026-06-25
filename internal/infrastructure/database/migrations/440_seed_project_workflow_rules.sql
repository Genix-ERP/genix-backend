-- Seed default project workflow rules for every existing tenant.
-- Each rule listens for a project event and creates an in-app notification
-- for the recipients resolved by the engine (data.notify_user_ids).
-- Conditions are empty ({}) so the rules always fire; tenants can edit or
-- disable them from the Workflows UI. NOT EXISTS guards keep this idempotent
-- and avoid clobbering a tenant that already has a rule for the same event.

-- 1) Task assigned
INSERT INTO workflow_rules (id, tenant_id, name, description, category, trigger_type, trigger_event, conditions, actions, is_active, priority, created_at, updated_at)
SELECT uuid_generate_v4(), t.id,
       'Vazifa biriktirilganda bildirishnoma',
       'Loyiha vazifasi xodimga biriktirilganda unga bildirishnoma yuboriladi',
       'project', 'event', 'project.task.assigned',
       '{}'::jsonb,
       '[{"type":"create_notification","config":{"type":"project_task_assigned","priority":"normal","title":"Yangi vazifa biriktirildi","message":"Sizga \"{task_title}\" vazifasi biriktirildi"}}]'::jsonb,
       true, 0, NOW(), NOW()
FROM tenants t
WHERE NOT EXISTS (
    SELECT 1 FROM workflow_rules wr
    WHERE wr.tenant_id = t.id AND wr.trigger_event = 'project.task.assigned' AND wr.deleted_at IS NULL
);

-- 2) Task overdue (fired by the scheduler)
INSERT INTO workflow_rules (id, tenant_id, name, description, category, trigger_type, trigger_event, conditions, actions, is_active, priority, created_at, updated_at)
SELECT uuid_generate_v4(), t.id,
       'Vazifa muddati o''tganda bildirishnoma',
       'Vazifaning bajarilish muddati o''tib ketganda mas''ul xodimga ogohlantirish yuboriladi',
       'project', 'scheduled', 'project.task.overdue',
       '{}'::jsonb,
       '[{"type":"create_notification","config":{"type":"project_task_overdue","priority":"high","title":"Vazifa muddati o‘tib ketdi","message":"\"{task_title}\" vazifasi muddatidan kechikdi ({due_date})"}}]'::jsonb,
       true, 0, NOW(), NOW()
FROM tenants t
WHERE NOT EXISTS (
    SELECT 1 FROM workflow_rules wr
    WHERE wr.tenant_id = t.id AND wr.trigger_event = 'project.task.overdue' AND wr.deleted_at IS NULL
);

-- 3) Task status changed
INSERT INTO workflow_rules (id, tenant_id, name, description, category, trigger_type, trigger_event, conditions, actions, is_active, priority, created_at, updated_at)
SELECT uuid_generate_v4(), t.id,
       'Vazifa holati o''zgarganda bildirishnoma',
       'Loyiha vazifasi holati o''zgartirilganda mas''ul xodimlarga bildirishnoma yuboriladi',
       'project', 'event', 'project.task.status_changed',
       '{}'::jsonb,
       '[{"type":"create_notification","config":{"type":"project_task_status_changed","priority":"normal","title":"Vazifa holati yangilandi","message":"\"{task_title}\" vazifasi holati: {new_status}"}}]'::jsonb,
       true, 0, NOW(), NOW()
FROM tenants t
WHERE NOT EXISTS (
    SELECT 1 FROM workflow_rules wr
    WHERE wr.tenant_id = t.id AND wr.trigger_event = 'project.task.status_changed' AND wr.deleted_at IS NULL
);

-- 4) Milestone completed
INSERT INTO workflow_rules (id, tenant_id, name, description, category, trigger_type, trigger_event, conditions, actions, is_active, priority, created_at, updated_at)
SELECT uuid_generate_v4(), t.id,
       'Bosqich yakunlanganda bildirishnoma',
       'Loyiha bosqichi yakunlanganda loyiha rahbariga bildirishnoma yuboriladi',
       'project', 'event', 'project.milestone.completed',
       '{}'::jsonb,
       '[{"type":"create_notification","config":{"type":"project_milestone_completed","priority":"normal","title":"Bosqich yakunlandi","message":"\"{milestone_title}\" bosqichi yakunlandi"}}]'::jsonb,
       true, 0, NOW(), NOW()
FROM tenants t
WHERE NOT EXISTS (
    SELECT 1 FROM workflow_rules wr
    WHERE wr.tenant_id = t.id AND wr.trigger_event = 'project.milestone.completed' AND wr.deleted_at IS NULL
);

-- 5) Project over budget (fired by the scheduler)
INSERT INTO workflow_rules (id, tenant_id, name, description, category, trigger_type, trigger_event, conditions, actions, is_active, priority, created_at, updated_at)
SELECT uuid_generate_v4(), t.id,
       'Loyiha byudjetdan oshganda bildirishnoma',
       'Loyiha xarajatlari byudjetdan oshib ketganda loyiha rahbariga ogohlantirish yuboriladi',
       'project', 'threshold', 'project.over_budget',
       '{}'::jsonb,
       '[{"type":"create_notification","config":{"type":"project_over_budget","priority":"high","title":"Byudjet oshib ketdi","message":"\"{project_name}\" loyihasi byudjetdan oshdi (sarflangan: {spent} / byudjet: {budget})"}}]'::jsonb,
       true, 0, NOW(), NOW()
FROM tenants t
WHERE NOT EXISTS (
    SELECT 1 FROM workflow_rules wr
    WHERE wr.tenant_id = t.id AND wr.trigger_event = 'project.over_budget' AND wr.deleted_at IS NULL
);
