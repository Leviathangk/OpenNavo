import { flushPromises, mount } from '@vue/test-utils';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { defineComponent, h } from 'vue';
import BasicTab from '../src/views/catalog/package-detail/modules/basic-tab.vue';
import { JOB_TYPES, TRIGGER_TYPES, jobLabel } from '../src/views/ops/job/modules/job-labels';

const api = vi.hoisted(() => ({ save: vi.fn() }));
vi.mock('@/service/api', () => ({ updatePackageMeta: api.save }));
vi.mock('@/hooks/business/auth', () => ({ useAuth: () => ({ hasAuth: () => true }) }));
vi.mock('@/locales', () => ({ $t: (key: string) => key }));
vi.mock('@/store/modules/app', () => ({ useAppStore: () => ({ locale: 'zh-CN' }) }));

const container = defineComponent({
  setup:
    (_, { slots }) =>
    () =>
      h('div', slots.default?.())
});
const button = defineComponent({
  emits: ['click'],
  setup:
    (_, { emit, slots }) =>
    () =>
      h('button', { onClick: () => emit('click') }, slots.default?.())
});
// Keep only value and update:value so tests can edit like users.
function field(name: string) {
  return defineComponent({
    props: { value: { type: null, default: null } },
    emits: ['update:value'],
    setup:
      (props, { emit }) =>
      () =>
        h('input', {
          'data-field': name,
          value: props.value ?? '',
          onInput: (event: Event) => {
            const raw = (event.target as HTMLInputElement).value;
            emit('update:value', name === 'size' ? (raw === '' ? null : Number(raw)) : raw || null);
          }
        })
  });
}

const detail = {
  id: 7,
  names: ['Visual Studio Code'],
  descEn: 'Open-source code editor',
  homepage: null,
  downloadUrl: null,
  downloadSize: 1000,
  tap: 'homebrew/cask',
  raw: {},
  hidden: false,
  editorChoice: false,
  meta: { developer: null, repoUrl: null, tags: [], notes: null, accentColor: '#336699' }
};

function tab() {
  return mount(BasicTab, {
    props: { detail: detail as never },
    global: {
      stubs: {
        NGrid: container,
        NGi: container,
        NDescriptions: container,
        NDescriptionsItem: container,
        NEllipsis: container,
        NCollapse: container,
        NCollapseItem: container,
        NForm: container,
        NFormItem: container,
        NText: container,
        NSwitch: true,
        NDynamicTags: true,
        NInput: true,
        NInputNumber: field('size'),
        NColorPicker: field('color'),
        NButton: button,
        PermissionGate: container
      }
    }
  });
}

async function save(wrapper: ReturnType<typeof tab>) {
  const buttons = wrapper.findAll('button');
  await buttons[buttons.length - 1].trigger('click');
  await flushPromises();
  return api.save.mock.calls.at(-1)?.[1] as Record<string, unknown>;
}

describe('Package details: size and accent', () => {
  beforeEach(() => {
    api.save.mockReset();
    api.save.mockResolvedValue({ data: null, error: undefined });
  });

  it('Omit untouched fields to avoid overwriting newer probe or icon-derived values', async () => {
    const body = await save(tab());
    expect(body).not.toHaveProperty('downloadSize');
    expect(body).not.toHaveProperty('accentColor');
    expect(body).toMatchObject({ hidden: false, editorChoice: false, tags: [] });
  });

  it('Submit edited sizes in bytes and uppercase accent colors', async () => {
    const wrapper = tab();
    await wrapper.find('[data-field="size"]').setValue('123456789');
    await wrapper.find('[data-field="color"]').setValue('#aabbcc');
    const body = await save(wrapper);
    expect(body.downloadSize).toBe(123456789);
    expect(body.accentColor).toBe('#AABBCC');
  });

  it('Cleared size and accent submit null', async () => {
    const wrapper = tab();
    await wrapper.find('[data-field="size"]').setValue('');
    const clear = wrapper
      .findAll('button')
      .find(item => item.text() === 'page.catalog.packageDetail.basic.accentColorClear');
    await clear!.trigger('click');
    const body = await save(wrapper);
    expect(body.downloadSize).toBeNull();
    expect(body.accentColor).toBeNull();
  });
});

describe('Job center: types', () => {
  it('Only retained jobs can be triggered; retired types display history only', () => {
    expect(TRIGGER_TYPES).toEqual([
      'catalog_sync',
      'analytics_sync',
      'snapshot_build',
      'changelog_schedule',
      'assets_download-size',
      'cleanup'
    ]);
    expect(TRIGGER_TYPES).not.toContain('enrich_schedule');
    expect(JOB_TYPES).toEqual(
      expect.arrayContaining([...TRIGGER_TYPES, 'enrich_schedule', 'translate_schedule', 'assets_icons'])
    );
  });

  it('Convert asynq colons to underscores, preserving unknown names', () => {
    expect(jobLabel('assets:download-size')).toBe('page.ops.job.types.assets_download-size');
    expect(jobLabel('catalog:sync')).toBe('page.ops.job.types.catalog_sync');
    expect(jobLabel('enrich:package')).toBe('enrich:package');
  });
});
