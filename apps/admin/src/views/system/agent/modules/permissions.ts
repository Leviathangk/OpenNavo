import type { Schemas } from '@/typings/api/opennavo';
import { $t } from '@/locales';

export type AgentPermission = Schemas['AgentPermission'];

/** Grantable Hermes permissions grouped in menu order (11 §2.2). */
export const PERMISSION_GROUPS: { key: string; codes: AgentPermission[] }[] = [
  { key: 'dashboard', codes: ['dashboard:view'] },
  {
    key: 'catalog',
    codes: [
      'catalog:package:view',
      'catalog:package:edit',
      'catalog:package:resync',
      'catalog:asset:upload',
      'catalog:category:edit'
    ]
  },
  {
    key: 'content',
    codes: [
      'content:collection:edit',
      'content:collection:publish',
      'content:article:edit',
      'content:article:publish',
      'content:feature:edit',
      'content:glossary:edit',
      'content:announcement:edit'
    ]
  },
  { key: 'changelog', codes: ['changelog:release:edit', 'translation:review'] },
  {
    key: 'ops',
    codes: [
      'ops:job:view',
      'ops:job:trigger',
      'ops:search:view',
      'ops:search:edit',
      'ops:feedback:handle',
      'agent:log:view',
      'content:revision:restore'
    ]
  },
  { key: 'release', codes: ['release:desktop:notes'] }
];

/**
 * Use the contract's grantable list: hide absent permissions; put ungrouped listed permissions under Other,
 * so future permissions are not omitted.
 */
export function groupPermissions(grantable: readonly AgentPermission[]) {
  const known = new Set<string>(PERMISSION_GROUPS.flatMap(group => group.codes));
  const groups = PERMISSION_GROUPS.map(group => ({
    key: group.key,
    codes: group.codes.filter(code => grantable.includes(code))
  })).filter(group => group.codes.length > 0);
  const others = grantable.filter(code => !known.has(code));
  if (others.length) groups.push({ key: 'other', codes: [...others] });
  return groups;
}

/** Replace colons with underscores for permission translation keys; display raw codes for untranslated new permissions. */
export function permissionLabel(code: string): string {
  const key = `page.system.agent.permissionLabels.${code.replace(/:/g, '_')}` as App.I18n.I18nKey;
  const text = $t(key);
  return text === key ? code : text;
}

export function groupLabel(key: string): string {
  return $t(`page.system.agent.permissionGroups.${key}` as App.I18n.I18nKey);
}

/** Hermes configuration (11 appendix B): store the token as OPENNAVO_TOKEN in ~/.hermes/.env; configuration references only the variable name. */
export function hermesConfig(mcpUrl: string): string {
  return [
    'mcp_servers:',
    '  opennavo:',
    `    url: "${mcpUrl}"`,
    '    headers:',
    '      Authorization: "Bearer ${OPENNAVO_TOKEN}"',
    '    timeout: 120',
    '    connect_timeout: 30'
  ].join('\n');
}
