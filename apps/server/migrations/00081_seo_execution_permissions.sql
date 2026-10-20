-- +goose Up
-- Extend future execution credentials only; existing tokens keep their grants.
CREATE TABLE seo_execution_policy_backup(id BOOLEAN PRIMARY KEY DEFAULT true CHECK(id), expression TEXT NOT NULL, defaults TEXT NOT NULL);
INSERT INTO seo_execution_policy_backup(expression,defaults)
 SELECT pg_get_expr(c.conbin,c.conrelid),pg_get_expr(d.adbin,d.adrelid)
 FROM pg_constraint c JOIN pg_attribute a ON a.attrelid=c.conrelid AND a.attname='permissions'
 JOIN pg_attrdef d ON d.adrelid=a.attrelid AND d.adnum=a.attnum
 WHERE c.conrelid='agent_tokens'::regclass AND c.conname='agent_tokens_permissions_check';
ALTER TABLE agent_tokens DROP CONSTRAINT agent_tokens_permissions_check;
ALTER TABLE agent_tokens ALTER COLUMN permissions SET DEFAULT ARRAY['seo:snippet:propose','seo:task:handle','seo:insight:view','seo:keyword:edit','dashboard:view','catalog:package:view','catalog:package:edit','catalog:package:resync','catalog:asset:upload','catalog:category:edit','content:collection:edit','content:collection:publish','content:article:edit','content:article:publish','content:feature:edit','content:glossary:edit','changelog:release:edit','translation:review','ops:job:view','ops:job:trigger','ops:search:view','ops:search:edit','ops:feedback:handle','agent:log:view','content:revision:restore','content:announcement:edit','release:desktop:notes']::text[];
ALTER TABLE agent_tokens ADD CONSTRAINT agent_tokens_permissions_check CHECK(permissions <@ ARRAY['seo:snippet:propose','seo:task:handle','seo:insight:view','seo:keyword:edit','dashboard:view','catalog:package:view','catalog:package:edit','catalog:package:resync','catalog:asset:upload','catalog:category:edit','content:collection:edit','content:collection:publish','content:article:edit','content:article:publish','content:feature:edit','content:glossary:edit','changelog:release:edit','translation:review','ops:job:view','ops:job:trigger','ops:search:view','ops:search:edit','ops:feedback:handle','agent:log:view','content:revision:restore','content:announcement:edit','release:desktop:notes']::text[] AND cardinality(permissions)<=27);
-- +goose Down
-- +goose StatementBegin
DO $$
DECLARE old_policy RECORD;
BEGIN
 SELECT * INTO STRICT old_policy FROM seo_execution_policy_backup;
 ALTER TABLE agent_tokens DROP CONSTRAINT agent_tokens_permissions_check;
 UPDATE agent_tokens SET permissions=array_remove(array_remove(array_remove(array_remove(permissions,'seo:snippet:propose'),'seo:task:handle'),'seo:insight:view'),'seo:keyword:edit');
 EXECUTE 'ALTER TABLE agent_tokens ALTER COLUMN permissions SET DEFAULT '||old_policy.defaults;
 EXECUTE 'ALTER TABLE agent_tokens ADD CONSTRAINT agent_tokens_permissions_check CHECK ('||old_policy.expression||')';
END $$;
-- +goose StatementEnd
DROP TABLE seo_execution_policy_backup;
