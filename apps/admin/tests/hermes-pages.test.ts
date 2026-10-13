import { flushPromises, mount } from '@vue/test-utils';
import { describe, expect, it, vi } from 'vitest';
import { defineComponent, h } from 'vue';
import TokenForm from '../src/views/system/agent/modules/token-form.vue';
import { buildTokenBody } from '../src/views/system/agent/modules/token';
import { PERMISSION_GROUPS, groupPermissions, hermesConfig } from '../src/views/system/agent/modules/permissions';
import type { AgentPermission } from '../src/views/system/agent/modules/permissions';
import {
  buildNotesBody,
  emptyNotes,
  normalizeSections,
  validateNotes
} from '../src/views/changelog/release/modules/notes';
import { defaultRetranslateLocales } from '../src/views/changelog/review/modules/locales';

const api = vi.hoisted(() => ({ create: vi.fn(), update: vi.fn() }));
vi.mock('@/locales', () => ({ $t: (key: string) => key }));
vi.mock('@/service/api', () => ({ createAgentToken: api.create, updateAgentToken: api.update }));

const ALL: AgentPermission[] = PERMISSION_GROUPS.flatMap(group => group.codes);

describe('Agent access: permissions and tokens', () => {
  it('All 21 grantable permissions are grouped, excluding system, mirrors, configuration, and desktop publishing', () => {
    expect(ALL).toHaveLength(21);
    expect(new Set(ALL).size).toBe(21);
    for (const forbidden of [
      'system:config:edit',
      'system:user:edit',
      'release:desktop:publish',
      'release:desktop:edit',
      'agent:manage'
    ]) {
      expect(ALL).not.toContain(forbidden);
    }
  });

  it('Use the contract list, hiding absent permissions and grouping new ones under Other', () => {
    const groups = groupPermissions(['catalog:package:view', 'future:new' as AgentPermission]);
    expect(groups).toEqual([
      { key: 'catalog', codes: ['catalog:package:view'] },
      { key: 'other', codes: ['future:new'] }
    ]);
  });

  it('Default expiry omitted, never null, custom ISO; remove empty IPs', () => {
    const base = {
      name: ' 每日更新 ',
      permissions: ALL,
      allowDelete: true,
      expiresAt: null,
      ipAllowlist: [' 203.0.113.0/24 ', ' ']
    };
    const byDefault = buildTokenBody({ ...base, expiry: 'default' });
    expect(byDefault).not.toHaveProperty('expiresAt');
    expect(byDefault).toMatchObject({ name: '每日更新', allowDelete: true, ipAllowlist: ['203.0.113.0/24'] });
    expect(byDefault.permissions).toHaveLength(21);
    expect(buildTokenBody({ ...base, expiry: 'never' }).expiresAt).toBeNull();
    const at = Date.UTC(2027, 0, 1);
    expect(buildTokenBody({ ...base, expiry: 'custom', expiresAt: at }).expiresAt).toBe('2027-01-01T00:00:00.000Z');
  });

  it('Connection configuration references environment variables without plaintext tokens', () => {
    const config = hermesConfig('https://admin.opennavo.example/mcp');
    expect(config).toContain('url: "https://admin.opennavo.example/mcp"');
    expect(config).toContain('${OPENNAVO_TOKEN}');
  });
});

describe('New token form', () => {
  const passthrough = defineComponent({
    setup:
      (_, { slots }) =>
      () =>
        h('div', [slots.default?.(), slots.footer?.()])
  });
  // Write checked state only to attributes for assertions.
  const checkboxGroup = defineComponent({
    props: { value: { type: Array, default: () => [] } },
    setup:
      (props, { slots }) =>
      () =>
        h('div', { 'data-picked': (props.value as string[]).join(',') }, slots.default?.())
  });
  const input = defineComponent({
    props: { value: { type: String, default: '' } },
    emits: ['update:value'],
    setup:
      (props, { emit }) =>
      () =>
        h('input', {
          value: props.value,
          onInput: (event: Event) => emit('update:value', (event.target as HTMLInputElement).value)
        })
  });
  const button = defineComponent({
    emits: ['click'],
    setup:
      (_, { emit, slots }) =>
      () =>
        h('button', { onClick: () => emit('click') }, slots.default?.())
  });

  it('Already-open mounting still selects all 21 permissions and submits default 90-day expiry', async () => {
    api.create.mockReset();
    api.create.mockResolvedValue({ data: { plaintext: 'MOCK_ONLY' }, error: undefined });
    const wrapper = mount(TokenForm, {
      props: { show: true, clientId: 7, token: null, grantable: ALL },
      global: {
        stubs: {
          NModal: passthrough,
          NForm: passthrough,
          NFormItem: passthrough,
          NText: passthrough,
          NCheckbox: passthrough,
          NCheckboxGroup: checkboxGroup,
          NInput: input,
          NButton: button,
          NSwitch: true,
          NRadioGroup: true,
          NRadio: true,
          NDatePicker: true,
          NDynamicTags: true
        }
      }
    });
    expect(wrapper.find('[data-picked]').attributes('data-picked')!.split(',')).toHaveLength(21);
    await wrapper.find('input').setValue('每日更新');
    const buttons = wrapper.findAll('button');
    await buttons[buttons.length - 1].trigger('click');
    await flushPromises();
    const [clientId, body] = api.create.mock.calls[0];
    expect(clientId).toBe(7);
    expect(body.permissions).toHaveLength(21);
    expect(body).not.toHaveProperty('expiresAt');
    expect(body).toMatchObject({ name: '每日更新', allowDelete: true, ipAllowlist: [] });
  });
});

describe('Author release notes', () => {
  const filled = () => ({
    ...emptyNotes('zh-CN'),
    summary: ' 修复启动崩溃 ',
    groups: [
      { area: ' 修复 ', items: [' 启动时崩溃 ', ''] },
      { area: '', items: [''] }
    ]
  });

  it('Remove empty highlights and groups', () => {
    expect(normalizeSections(filled().groups)).toEqual([{ area: '修复', items: ['启动时崩溃'] }]);
  });

  it('Validate required summary and contract limits: eight groups, six items, 12-character names, 80-character items', () => {
    expect(validateNotes(emptyNotes())).toBe('page.changelog.notes.errors.summaryRequired');
    expect(validateNotes(filled())).toBeNull();
    const many = {
      ...filled(),
      groups: Array.from({ length: 9 }, (_, index) => ({ area: `组${index}`, items: ['要点'] }))
    };
    expect(validateNotes(many)).toBe('page.changelog.notes.errors.tooManyGroups');
    const items = { ...filled(), groups: [{ area: '改进', items: Array.from({ length: 7 }, () => '要点') }] };
    expect(validateNotes(items)).toBe('page.changelog.notes.errors.tooManyItems');
    expect(validateNotes({ ...filled(), groups: [{ area: '一二三四五六七八九十一二三', items: ['要点'] }] })).toBe(
      'page.changelog.notes.errors.areaTooLong'
    );
    expect(validateNotes({ ...filled(), groups: [{ area: '改进', items: ['字'.repeat(81)] }] })).toBe(
      'page.changelog.notes.errors.itemTooLong'
    );
    expect(validateNotes({ ...filled(), groups: [{ area: '', items: ['要点'] }] })).toBe(
      'page.changelog.notes.errors.areaRequired'
    );
  });

  it('Payload sets clear false and blank title/body/date null', () => {
    const body = buildNotesBody(filled());
    expect(body).toEqual({
      clear: false,
      sourceLocale: 'zh-CN',
      summary: '修复启动崩溃',
      sections: [{ area: '修复', items: ['启动时崩溃'] }],
      title: null,
      bodyMarkdown: null,
      publishedAt: null
    });
  });
});

describe('Default retranslation selections', () => {
  const state = (status: string, stale = false) => ({ status, stale, fields: {} });
  const item = (overrides: Record<string, ReturnType<typeof state>>) =>
    ({
      sourceLocale: 'zh-CN',
      i18n: {
        'zh-CN': state('source'),
        'en-US': state('machine'),
        'ja-JP': state('machine'),
        'es-ES': state('machine'),
        'pt-BR': state('machine'),
        'ru-RU': state('machine'),
        ...overrides
      }
    }) as never;

  it('Select failed, missing, or source-changed locales only', () => {
    expect(defaultRetranslateLocales(item({ 'ja-JP': state('failed'), 'ru-RU': state('manual', true) }))).toEqual([
      'ja-JP',
      'ru-RU'
    ]);
  });

  it('Select all non-source locales when none need repair', () => {
    expect(defaultRetranslateLocales(item({}))).toEqual(['en-US', 'ja-JP', 'es-ES', 'pt-BR', 'ru-RU']);
  });
});
