import { flushPromises, mount } from '@vue/test-utils';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { defineComponent, h } from 'vue';
import MirrorSettings from '@/components/settings/MirrorSettings.vue';
import CustomMirrorForm from '@/components/settings/CustomMirrorForm.vue';
import { customMirrorInput, customMirrorProbes, validateMirrorDraft } from '@/composables/customMirrors';
import { useMirrorOptions } from '@/composables/useMirrorOptions';
import { i18n } from '@/i18n';
import type { MirrorProbe } from '@/ipc/bindings';
import { commands } from '@/ipc/client';
import { startLocalState, useSettingsStore } from '@/stores';
import { setup } from './helpers';
import customProbeContract from './fixtures/custom-mirror-probes.json';

// Settings download sources and shared source list (first-launch-onboarding §3.3, D2).
const remote = vi.hoisted(() => ({ config: null as unknown }));
vi.mock('@/composables/useClientConfig', () => ({
  fetchClientConfig: async () => {
    if (!remote.config) throw new Error('offline');
    return remote.config;
  }
}));

const mirror = (key: string, recommended = false) => ({
  key,
  name: key.toUpperCase(),
  probeUrl: `https://${key}.example/api/formula.jws.json`,
  recommended,
  apiDomain: `https://${key}.example/api`,
  bottleDomain: `https://${key}.example/bottles`,
  brewGitRemote: `https://${key}.example/brew.git`,
  coreGitRemote: `https://${key}.example/core.git`
});

const radios = () => [...document.body.querySelectorAll<HTMLButtonElement>('[role="radio"]')];

beforeEach(() => {
  remote.config = { mirrors: [mirror('tuna', true), mirror('ustc')] };
});

afterEach(() => {
  document.body.innerHTML = '';
  vi.restoreAllMocks();
});

async function mountSettings() {
  const context = await setup('/settings/mirrors');
  await startLocalState();
  const wrapper = mount(MirrorSettings, {
    global: { plugins: [context.pinia, context.router, i18n] },
    attachTo: document.body
  });
  await flushPromises();
  return wrapper;
}

describe('Settings: download sources', () => {
  it('Prepend built-in official when absent, distinguish fastest/recommended, save full selected URLs', async () => {
    await mountSettings();
    expect(radios().map(item => item.textContent)).toEqual([
      expect.stringContaining('官方源'),
      expect.stringContaining('TUNA'),
      expect.stringContaining('USTC')
    ]);
    const [official, tuna, ustc] = radios();
    expect(official?.getAttribute('aria-checked')).toBe('true');
    expect(tuna?.textContent).toContain('最快');
    expect(tuna?.textContent).toContain('推荐');
    expect(ustc?.textContent).not.toContain('推荐');
    ustc?.click();
    await flushPromises();
    expect(useSettingsStore().value?.mirror).toEqual({
      key: 'ustc',
      apiDomain: 'https://ustc.example/api',
      bottleDomain: 'https://ustc.example/bottles',
      brewGitRemote: 'https://ustc.example/brew.git',
      coreGitRemote: 'https://ustc.example/core.git'
    });
  });

  it('Official remains selectable with mirror-load errors and retry guidance', async () => {
    remote.config = null;
    await mountSettings();
    expect(radios()).toHaveLength(1);
    expect(document.body.textContent).toContain('暂时拿不到镜像列表');
    remote.config = { mirrors: [mirror('tuna')] };
    [...document.body.querySelectorAll('button')].find(item => item.textContent?.trim() === '重试')?.click();
    await vi.waitFor(() => expect(radios()).toHaveLength(2));
  });
});

describe('Settings: copy terminal environment', () => {
  const envText = () => document.body.querySelector('pre')?.textContent;

  afterEach(() => {
    Reflect.deleteProperty(navigator, 'clipboard');
  });

  // jsdom has no clipboard: use an observable implementation; copied text must match the displayed text.
  async function clickCopy() {
    const writeText = vi.fn().mockResolvedValue(undefined);
    Object.defineProperty(navigator, 'clipboard', { configurable: true, value: { writeText } });
    [...document.body.querySelectorAll('button')].find(item => item.textContent?.includes('复制终端环境变量'))?.click();
    await flushPromises();
    return writeText;
  }

  it('Official source unsets four variables on separate lines', async () => {
    await mountSettings();
    const expected = [
      'unset HOMEBREW_API_DOMAIN',
      'unset HOMEBREW_BOTTLE_DOMAIN',
      'unset HOMEBREW_BREW_GIT_REMOTE',
      'unset HOMEBREW_CORE_GIT_REMOTE'
    ].join('\n');
    expect(envText()).toBe(expected);
    expect(await clickCopy()).toHaveBeenCalledWith(expected);
  });

  it('Mirrors export one variable per line with single-quoted URLs', async () => {
    await mountSettings();
    radios()[2]?.click();
    await flushPromises();
    const expected = [
      "export HOMEBREW_API_DOMAIN='https://ustc.example/api'",
      "export HOMEBREW_BOTTLE_DOMAIN='https://ustc.example/bottles'",
      "export HOMEBREW_BREW_GIT_REMOTE='https://ustc.example/brew.git'",
      "export HOMEBREW_CORE_GIT_REMOTE='https://ustc.example/core.git'"
    ].join('\n');
    expect(envText()).toBe(expected);
    expect(await clickCopy()).toHaveBeenCalledWith(expected);
  });

  it('Export only configured URLs and escape embedded quotes', async () => {
    await mountSettings();
    await useSettingsStore().update('mirror', {
      key: 'custom',
      apiDomain: "https://mirror.example/api?q='value'",
      bottleDomain: null,
      brewGitRemote: null,
      coreGitRemote: null
    });
    await flushPromises();
    expect(envText()).toBe("export HOMEBREW_API_DOMAIN='https://mirror.example/api?q='\\''value'\\'''");
  });
});

describe('Slow official-source suggestions', () => {
  async function suggestionFor(probes: MirrorProbe[]) {
    await setup();
    vi.spyOn(commands, 'mirrorProbe').mockResolvedValue({ status: 'ok', data: probes });
    let state: ReturnType<typeof useMirrorOptions> | undefined;
    const Probe = defineComponent({
      setup() {
        state = useMirrorOptions();
        return () => h('div');
      }
    });
    mount(Probe, { global: { plugins: [i18n] } });
    await state?.load();
    await state?.probe();
    return state?.suggestion.value;
  }
  const result = (key: string, latencyMs: number | null): MirrorProbe => ({
    key,
    ok: latencyMs !== null,
    latencyMs,
    status: latencyMs === null ? null : 200,
    error: latencyMs === null ? 'timeout' : null
  });

  it('Suggest fastest mirror when official exceeds 1,500 ms and mirrors are faster', async () => {
    const suggestion = await suggestionFor([result('official', 2400), result('tuna', 40), result('ustc', 30)]);
    expect(suggestion?.option.key).toBe('ustc');
    expect(suggestion?.officialMs).toBe(2400);
  });

  it('Suggest a reachable mirror when official probing fails', async () => {
    const suggestion = await suggestionFor([result('official', null), result('tuna', 40), result('ustc', null)]);
    expect(suggestion?.option.key).toBe('tuna');
    expect(suggestion?.officialMs).toBeNull();
  });

  it('No suggestion below 1,500 ms or when mirrors fail/are slower', async () => {
    expect(await suggestionFor([result('official', 1500), result('tuna', 40)])).toBeUndefined();
    expect(await suggestionFor([result('official', 2400), result('tuna', null)])).toBeUndefined();
    expect(await suggestionFor([result('official', 1600), result('tuna', 1800)])).toBeUndefined();
  });
});

describe('Local custom download mirrors', () => {
  it('Matches the four-endpoint payload exercised by the native IPC command test', () => {
    expect(customMirrorProbes(customProbeContract.source)).toEqual(customProbeContract.probes);
  });

  const draft = (apiDomain = 'https://local.example/api') => ({
    name: 'My mirror',
    apiDomain,
    bottleDomain: '',
    brewGitRemote: '',
    coreGitRemote: ''
  });

  it('Saves canonical API bases and derives probes exactly as Homebrew concatenates endpoints', () => {
    for (const example of customProbeContract.apiBases) {
      const input = customMirrorInput(draft(example.input), 'custom-test');
      expect(input.apiDomain).toBe(example.normalized);
      expect(input.probeUrl).toBe(`${input.apiDomain}/cask.jws.json`);
      expect(input.probeUrl).toBe(example.probeUrl);
      expect(customMirrorProbes(input)[0]?.probeUrl).toBe(example.probeUrl);
    }
    for (const api of customProbeContract.invalidApiBases) {
      expect(validateMirrorDraft(draft(api)).apiDomain).toBe('apiQueryInvalid');
      expect(() => customMirrorInput(draft(api), 'custom-test')).toThrow();
    }
  });

  it('Blocks checking and saving an API URL with a query before IPC', async () => {
    await setup();
    const probe = vi.spyOn(commands, 'mirrorProbe');
    const wrapper = mount(CustomMirrorForm, {
      props: { saving: false },
      global: { plugins: [i18n] }
    });
    await wrapper.findAll('input')[0]!.setValue('Local');
    await wrapper.findAll('input')[1]!.setValue('https://mirror.example/api?channel=stable');
    await wrapper
      .findAll('button')
      .find(item => item.text() === '检查连接')!
      .trigger('click');
    await flushPromises();
    expect(probe).not.toHaveBeenCalled();
    expect(wrapper.find('[role="alert"]').text()).toContain('API 地址不能包含查询参数');
    await wrapper.find('form').trigger('submit');
    await flushPromises();
    expect(wrapper.emitted('save')).toBeUndefined();
    await wrapper.findAll('input')[1]!.setValue('https://mirror.example/api///');
    await wrapper.find('form').trigger('submit');
    await flushPromises();
    expect(wrapper.emitted('save')?.[0]?.[0]).toMatchObject({
      apiDomain: 'https://mirror.example/api',
      probeUrl: 'https://mirror.example/api/cask.jws.json'
    });
    wrapper.unmount();
  });

  it('Validates addresses and checks all configured endpoints with stable API paths', () => {
    for (const url of [
      'file:///tmp/mirror',
      'https://user:password@example.test',
      'https://example.test/#fragment',
      'https://example.test/\n'
    ]) {
      expect(validateMirrorDraft(draft(url)).apiDomain).toBe('urlInvalid');
    }
    expect(validateMirrorDraft({ ...draft(''), name: '' })).toEqual({
      name: 'nameRequired',
      addresses: 'addressRequired'
    });
    const input = customMirrorInput(
      { ...draft('https://local.example/api///'), bottleDomain: 'https://local.example/bottles' },
      'custom-test'
    );
    expect(customMirrorProbes(input).map(item => item.probeUrl)).toEqual([
      'https://local.example/api/cask.jws.json',
      'https://local.example/bottles'
    ]);
  });

  it('Creates, selects, reloads and edits a custom mirror atomically; deletion returns to official', async () => {
    const wrapper = await mountSettings();
    await wrapper
      .findAll('button')
      .find(item => item.text() === '添加自定义源')!
      .trigger('click');
    const form = wrapper.findComponent(CustomMirrorForm);
    await form.findAll('input')[0]!.setValue('My mirror');
    await form.findAll('input')[1]!.setValue('https://local.example/api');
    await form.find('form').trigger('submit');
    await flushPromises();
    const store = useSettingsStore();
    const saved = store.value!.customMirrors[0]!;
    expect(saved.name).toBe('My mirror');
    expect(store.value!.mirror.key).toBe(saved.key);
    expect(store.value!.mirror.bottleDomain).toBeNull();
    expect(radios().find(item => item.textContent?.includes('My mirror'))?.textContent).toContain('仅本机');
    await store.load();
    expect(store.value!.customMirrors).toEqual([saved]);
    await store.saveCustomMirror({
      ...saved,
      name: 'Renamed',
      apiDomain: 'https://new.example/api'
    });
    expect(store.value!.mirror.apiDomain).toBe('https://new.example/api');
    await store.removeCustomMirror(saved.key);
    expect(store.value!.customMirrors).toEqual([]);
    expect(store.value!.mirror).toEqual({
      key: 'official',
      apiDomain: null,
      bottleDomain: null,
      brewGitRemote: null,
      coreGitRemote: null
    });
    wrapper.unmount();
  });

  it('Failed local save preserves the existing source and custom list', async () => {
    const wrapper = await mountSettings();
    const store = useSettingsStore();
    const previous = JSON.parse(JSON.stringify(store.value));
    vi.spyOn(commands, 'settingsSet').mockResolvedValue({
      status: 'error',
      error: { code: 'E_UNKNOWN', message: 'disk_full', detail: null }
    });
    await expect(store.saveCustomMirror(customMirrorInput(draft(), 'custom-test'), true)).rejects.toBeDefined();
    expect(store.value).toEqual(previous);
    wrapper.unmount();
  });

  it('API success does not hide Bottle failure and editing invalidates old check results', async () => {
    await setup();
    const wrapper = mount(CustomMirrorForm, {
      props: { saving: false },
      global: { plugins: [i18n] },
      attachTo: document.body
    });
    await wrapper.findAll('input')[0]!.setValue('My mirror');
    await wrapper.findAll('input')[1]!.setValue('https://local.example/api');
    await wrapper.findAll('input')[2]!.setValue('https://local.example/bottles');
    vi.spyOn(commands, 'mirrorProbe').mockImplementation(async inputs => ({
      status: 'ok',
      data: inputs.map(input => ({
        key: input.key,
        ok: input.name === 'apiDomain',
        latencyMs: 5,
        status: input.name === 'apiDomain' ? 200 : 404,
        error: null
      }))
    }));
    await wrapper
      .findAll('button')
      .find(item => item.text() === '检查连接')!
      .trigger('click');
    await flushPromises();
    expect(wrapper.find('[role="status"]').text()).toContain('Bottle');
    await wrapper.findAll('input')[2]!.setValue('https://different.example/bottles');
    expect(wrapper.find('[role="status"]').text()).toBe('尚未检查');
    wrapper.unmount();
  });
});

describe('Custom sources with unavailable built-in configuration', () => {
  it('Keeps a local source selectable when the remote mirror list cannot load', async () => {
    remote.config = null;
    const wrapper = await mountSettings();
    const input = customMirrorInput(
      {
        name: 'Offline local',
        apiDomain: '',
        bottleDomain: 'https://local.example/bottles',
        brewGitRemote: '',
        coreGitRemote: ''
      },
      'custom-offline'
    );
    await useSettingsStore().saveCustomMirror(input);
    await flushPromises();
    expect(radios()).toHaveLength(2);
    radios()
      .find(item => item.textContent?.includes('Offline local'))!
      .click();
    await flushPromises();
    expect(useSettingsStore().value?.mirror.key).toBe(input.key);
    expect(wrapper.text()).toContain('仍可使用官方源和本机自定义源');
    wrapper.unmount();
  });

  it('Discards a completed probe if its addresses changed during the request', async () => {
    await setup();
    const wrapper = mount(CustomMirrorForm, {
      props: { saving: false },
      global: { plugins: [i18n] }
    });
    await wrapper.findAll('input')[0]!.setValue('Local');
    await wrapper.findAll('input')[1]!.setValue('https://local.example/api');
    let finish!: (value: Awaited<ReturnType<typeof commands.mirrorProbe>>) => void;
    const pending = new Promise<Awaited<ReturnType<typeof commands.mirrorProbe>>>(resolve => {
      finish = resolve;
    });
    vi.spyOn(commands, 'mirrorProbe').mockReturnValue(pending);
    await wrapper
      .findAll('button')
      .find(item => item.text() === '检查连接')!
      .trigger('click');
    await flushPromises();
    await wrapper.findAll('input')[1]!.setValue('https://changed.example/api');
    finish({ status: 'ok', data: [] });
    await flushPromises();
    expect(wrapper.find('[role="status"]').text()).toBe('尚未检查');
    wrapper.unmount();
  });
});
