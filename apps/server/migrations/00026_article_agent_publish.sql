-- +goose Up
-- Make publication grantable without changing any existing token's permissions.
INSERT INTO admin_role_permissions(role_id, permission_code)
SELECT id, 'content:article:publish' FROM admin_roles WHERE role_code = 'R_AGENT'
ON CONFLICT DO NOTHING;

-- Update both the database constraint and defaults; older migrations excluded article editing too.
ALTER TABLE agent_tokens DROP CONSTRAINT agent_tokens_permissions_check;
ALTER TABLE agent_tokens ALTER COLUMN permissions SET DEFAULT ARRAY['dashboard:view','catalog:package:view','catalog:package:edit','catalog:package:resync','catalog:asset:upload','catalog:category:edit','content:collection:edit','content:collection:publish','content:article:edit','content:article:publish','content:feature:edit','content:glossary:edit','changelog:release:edit','translation:review','ops:job:view','ops:job:trigger','ops:search:view','ops:search:edit','ops:feedback:handle','agent:log:view','content:revision:restore','content:announcement:edit','release:desktop:notes']::text[];
ALTER TABLE agent_tokens ADD CONSTRAINT agent_tokens_permissions_check
CHECK (permissions <@ ARRAY['dashboard:view','catalog:package:view','catalog:package:edit','catalog:package:resync','catalog:asset:upload','catalog:category:edit','content:collection:edit','content:collection:publish','content:article:edit','content:article:publish','content:feature:edit','content:glossary:edit','changelog:release:edit','translation:review','ops:job:view','ops:job:trigger','ops:search:view','ops:search:edit','ops:feedback:handle','agent:log:view','content:revision:restore','content:announcement:edit','release:desktop:notes']::text[] AND cardinality(permissions) <= 23);

-- +goose Down
-- Remove newly supported grants before reinstating the old credential bounds.
UPDATE agent_tokens SET permissions = array_remove(array_remove(permissions, 'content:article:publish'), 'content:article:edit');
ALTER TABLE agent_tokens DROP CONSTRAINT agent_tokens_permissions_check;
ALTER TABLE agent_tokens ALTER COLUMN permissions SET DEFAULT ARRAY['dashboard:view','catalog:package:view','catalog:package:edit','catalog:package:resync','catalog:asset:upload','catalog:category:edit','content:collection:edit','content:collection:publish','content:feature:edit','content:glossary:edit','changelog:release:edit','translation:review','ops:job:view','ops:job:trigger','ops:search:view','ops:search:edit','ops:feedback:handle','agent:log:view','content:revision:restore','content:announcement:edit','release:desktop:notes']::text[];
ALTER TABLE agent_tokens ADD CONSTRAINT agent_tokens_permissions_check
CHECK (permissions <@ ARRAY['dashboard:view','catalog:package:view','catalog:package:edit','catalog:package:resync','catalog:asset:upload','catalog:category:edit','content:collection:edit','content:collection:publish','content:feature:edit','content:glossary:edit','changelog:release:edit','translation:review','ops:job:view','ops:job:trigger','ops:search:view','ops:search:edit','ops:feedback:handle','agent:log:view','content:revision:restore','content:announcement:edit','release:desktop:notes']::text[] AND cardinality(permissions) <= 21);

DELETE FROM admin_role_permissions
WHERE permission_code = 'content:article:publish'
  AND role_id IN (SELECT id FROM admin_roles WHERE role_code = 'R_AGENT');
