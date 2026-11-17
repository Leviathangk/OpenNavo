import { mount } from '@vue/test-utils';
import { createPinia, setActivePinia } from 'pinia';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { commands } from '@/ipc/client';
import { useHermesStore } from '@/stores/hermes';
import HermesSettings from '@/components/hermes/HermesSettings.vue';
import { i18n } from '@/i18n';

const ok = <T>(data: T) => ({ status: 'ok' as const, data });

const PROFILES_KEY = 'ONV_HERMES_PROFILES_V1';

/** Shape captured from a real Hermes Panel host (`GET /api/model/options`). */
const PANEL_PAYLOAD = {
  providers: [
    {
      slug: 'custom',
      name: 'Custom',
      is_current: true,
      is_user_defined: false,
      models: ['grok-4.7'],
      total_models: 1,
      source: 'configured-current',
      authenticated: false,
      auth_type: 'api_key',
      warning:
        'Configured provider is not authenticated; run `hermes model` to reactivate. Showing the saved model only.'
    },
    {
      slug: 'custom:newapi',
      name: 'newapi',
      is_current: true,
      is_user_defined: true,
      models: ['grok-4.6', 'grok-4.3', 'gpt-6-sol', 'deepseek-v4.1-flash'],
      total_models: 4,
      source: 'user-config',
      authenticated: true
    }
  ]
};

const SAVED_CATALOG = {
  providers: [
    {
      slug: 'openavoo-custom',
      name: 'Custom',
      is_current: true,
      models: ['saved-model', 'grok-default'],
      total_models: 2,
      authenticated: true
    }
  ]
};

const RESEARCH_CATALOG = {
  providers: [
    {
      slug: 'openavoo-custom',
      name: 'Custom',
      is_current: true,
      models: ['grok-research'],
      total_models: 1,
      authenticated: true
    }
  ]
};

function seedConnection(kind: 'local' | 'panel') {
  if (kind === 'panel') {
    localStorage.setItem(
      PROFILES_KEY,
      JSON.stringify({
        version: 1,
        activeProfileId: 'panel-1',
        profiles: [
          { id: 'local', name: 'Local', kind: 'local', endpoint: '' },
          { id: 'panel-1', name: 'Panel', kind: 'panel', endpoint: 'http://panel.tail1234.ts.net:9119' }
        ]
      })
    );
  }
}

const AGENT_PROFILES = [
  {
    name: 'default',
    displayName: null,
    path: '/tmp/hermes',
    isDefault: true,
    model: 'gpt-5.4',
    provider: 'openai-api',
    skillCount: 0,
    hasEnv: false,
    description: null,
    botTitle: null,
    distributionName: null,
    distributionVersion: null
  },
  {
    name: 'research',
    displayName: 'Research',
    path: '/tmp/hermes/profiles/research',
    isDefault: false,
    model: 'claude-sonnet-4.5',
    provider: 'anthropic',
    skillCount: 2,
    hasEnv: true,
    description: null,
    botTitle: null,
    distributionName: null,
    distributionVersion: null
  }
];

/** The profile-scope `<select>` in the sheet header, not a model picker. */
function profileScopeSelect(): HTMLSelectElement | null {
  return (
    Array.from(document.body.querySelectorAll('select')).find(select =>
      Array.from(select.options).some(option => option.value === 'research')
    ) ?? null
  );
}

function cacheKeysForProfile(connectionId: string, agentProfile: string): string[] {
  const prefix = `ONV_HERMES_MODEL_OPTIONS_V3::${encodeURIComponent(connectionId)}::${encodeURIComponent(agentProfile)}::`;
  const keys: string[] = [];
  for (let index = 0; index < localStorage.length; index += 1) {
    const key = localStorage.key(index);
    if (key?.startsWith(prefix)) keys.push(key);
  }
  return keys;
}

async function mountSheet() {
  // The sheet loads on the open transition, so start closed and open it.
  const wrapper = mount(HermesSettings, {
    props: { open: false, tab: 'model' },
    attachTo: document.body,
    global: { plugins: [i18n] }
  });
  await wrapper.setProps({ open: true });
  await vi.waitFor(() => expect(commands.hermesModelOptions).toHaveBeenCalled());
  await new Promise(resolve => setTimeout(resolve, 0));
  return wrapper;
}

/** The sheet teleports into `body`, so assertions read the document, not the wrapper. */
const sheetText = () => document.body.textContent ?? '';
const hasAuthField = () => document.body.querySelector('#hermes-auth') !== null;

describe('Hermes settings credentials', () => {
  let wrapper: ReturnType<typeof mount> | null = null;

  afterEach(() => {
    // Every sheet is attached to `body`; leaving one mounted leaks its copy into the next test.
    wrapper?.unmount();
    wrapper = null;
    document.body.innerHTML = '';
  });

  beforeEach(() => {
    localStorage.clear();
    setActivePinia(createPinia());
    Object.defineProperty(window, '__TAURI_INTERNALS__', { value: {}, configurable: true });
    vi.spyOn(commands, 'hermesRuntimeStatus').mockResolvedValue(
      ok({
        ready: true,
        installed: true,
        version: '0.23.0',
        expectedVersion: '0.23.0',
        path: '/tmp/hermes',
        dataPath: '/tmp',
        computerUse: false,
        outdated: false,
        message: null
      })
    );
    vi.spyOn(commands, 'hermesLoginStatus').mockResolvedValue(
      ok({
        codex: false,
        claudeCode: false,
        copilot: false,
        opencode: false,
        opencodeGo: false,
        nous: false,
        codexSource: null,
        claudeCodeSource: null,
        copilotSource: null,
        opencodeSource: null,
        opencodeGoSource: null,
        nousSource: null
      })
    );
    vi.spyOn(commands, 'hermesSecretStatus').mockResolvedValue(ok(false));
    vi.spyOn(commands, 'hermesModelOptions').mockResolvedValue(ok(PANEL_PAYLOAD));
    vi.spyOn(commands, 'hermesPermissionsStatus').mockResolvedValue(ok({} as never));
  });

  it('Hides this Mac’s credential fields on a panel connection and points at the host', async () => {
    seedConnection('panel');
    useHermesStore().useProvider('custom');
    await mountSheet();

    expect(hasAuthField()).toBe(false);
    // Copy comes from `content.ts`; the sheet renders en-US in tests.
    expect(sheetText()).toContain('panel host');
  });

  it('Shows the model list of the endpoint the panel host actually serves', async () => {
    seedConnection('panel');
    useHermesStore().useProvider('custom');
    wrapper = await mountSheet();

    const rows = useHermesStore().modelOptions?.providers ?? [];
    const endpoint = rows.find(row => row.slug === 'custom:newapi');
    expect(endpoint?.models).toHaveLength(4);

    // The unauthenticated `custom` row's warning must not be presented as this card's status.
    expect(sheetText()).not.toContain('not authenticated');
    // And the endpoint's own models are what the picker offers.
    for (const id of ['grok-4.6', 'grok-4.3', 'gpt-6-sol', 'deepseek-v4.1-flash']) {
      expect(JSON.stringify(rows.find(row => row.slug === 'custom:newapi')?.models)).toContain(id);
    }
  });

  it('Keeps the credential fields on a local connection', async () => {
    seedConnection('local');
    useHermesStore().useProvider('custom');
    wrapper = await mountSheet();

    expect(hasAuthField()).toBe(true);
    expect(sheetText()).not.toContain('panel host');
  });

  it('Does not carry a key saved for another agent profile into the active one', async () => {
    seedConnection('local');
    vi.spyOn(commands, 'hermesAgentProfiles').mockResolvedValue(ok(AGENT_PROFILES));
    const secretSet = vi.spyOn(commands, 'hermesSecretSet').mockResolvedValue(ok(null));
    vi.spyOn(commands, 'hermesSecretStatus').mockResolvedValue(ok(true));
    const store = useHermesStore();
    await store.loadAgentProfiles();
    // Give the profile being edited its own provider so the sheet keeps a key field after the
    // scope changes (a login-based provider would swap in the OAuth panel instead).
    store.setAgentProfileModel('research', {
      ...store.model,
      provider: 'custom',
      model: 'grok-4.7',
      endpoint: 'https://api.example.test/v1',
      keySet: false
    });
    store.useProvider('custom');
    wrapper = await mountSheet();

    const select = profileScopeSelect();
    expect(select).not.toBeNull();
    select!.value = 'research';
    select!.dispatchEvent(new Event('change'));
    await new Promise(resolve => setTimeout(resolve, 0));

    const key = document.body.querySelector<HTMLInputElement>('#hermes-key');
    expect(key).not.toBeNull();
    key!.value = 'sk-research-only';
    key!.dispatchEvent(new Event('input'));

    const save = Array.from(document.body.querySelectorAll('button')).find(
      button => button.textContent?.trim() === 'Save'
    );
    expect(save).toBeDefined();
    save!.click();
    await new Promise(resolve => setTimeout(resolve, 0));

    // Written to the profile the sheet was editing...
    expect(secretSet).toHaveBeenCalledWith('custom', 'sk-research-only', 'research');
    // ...and not kept in the store, where the active profile's next turn would copy it into
    // its own `.env` through `write_config`.
    expect(store.activeAgentProfileName).toBe('default');
    expect(store.apiKey).toBe('');
  });

  it('Drops the cached catalog for the profile whose API key was written', async () => {
    seedConnection('local');
    vi.spyOn(commands, 'hermesAgentProfiles').mockResolvedValue(ok(AGENT_PROFILES));
    vi.spyOn(commands, 'hermesSecretSet').mockResolvedValue(ok(null));
    vi.spyOn(commands, 'hermesSecretStatus').mockResolvedValue(ok(true));
    const store = useHermesStore();
    await store.loadAgentProfiles();
    store.setAgentProfileModel('research', {
      ...store.model,
      provider: 'custom',
      model: 'grok-4.7',
      endpoint: 'https://api.example.test/v1',
      keySet: false
    });
    store.useProvider('custom');
    wrapper = await mountSheet();

    const select = profileScopeSelect();
    expect(select).not.toBeNull();
    select!.value = 'research';
    select!.dispatchEvent(new Event('change'));
    await vi.waitFor(() => expect(cacheKeysForProfile('local', 'research')).toHaveLength(1));

    const key = document.body.querySelector<HTMLInputElement>('#hermes-key');
    expect(key).not.toBeNull();
    key!.value = 'sk-research-only';
    key!.dispatchEvent(new Event('input'));

    const save = Array.from(document.body.querySelectorAll('button')).find(
      button => button.textContent?.trim() === 'Save'
    );
    expect(save).toBeDefined();
    save!.click();

    await vi.waitFor(() => expect(cacheKeysForProfile('local', 'research')).toHaveLength(0));
  });

  it('Does not align the model being edited from another profile catalog', async () => {
    seedConnection('panel');
    vi.spyOn(commands, 'hermesAgentProfiles').mockResolvedValue(ok(AGENT_PROFILES));
    vi.mocked(commands.hermesModelOptions).mockImplementation(input =>
      Promise.resolve(ok(input.profile === 'research' ? RESEARCH_CATALOG : SAVED_CATALOG))
    );
    const store = useHermesStore();
    await store.loadAgentProfiles();
    store.useProvider('custom');
    store.saveModel({ ...store.model, provider: 'custom', model: 'saved-model' });
    store.setAgentProfileModel('research', { ...store.model, model: 'grok-research' });
    wrapper = await mountSheet();
    await vi.waitFor(() => expect(store.modelOptionsForScope('default', store.model)).not.toBeNull());
    expect(document.body.querySelector<HTMLInputElement>('#hermes-model')?.value).toBe('saved-model');

    const select = profileScopeSelect();
    expect(select).not.toBeNull();
    select!.value = 'research';
    select!.dispatchEvent(new Event('change'));
    await vi.waitFor(() => expect(store.modelOptionsForScope('research', store.model)).not.toBeNull());
    expect(document.body.querySelector<HTMLInputElement>('#hermes-model')?.value).toBe('grok-research');

    await wrapper.setProps({ open: false });
    store.invalidateModelOptionsCache('default');
    expect(store.model.model).toBe('saved-model');
    vi.mocked(commands.hermesModelOptions).mockImplementation(input =>
      input.profile === 'research' ? Promise.resolve(ok(RESEARCH_CATALOG)) : Promise.reject(new Error('panel offline'))
    );
    await wrapper.setProps({ open: true });
    await vi.waitFor(() => expect(sheetText()).toContain('panel offline'));

    const model = document.body.querySelector<HTMLInputElement>('#hermes-model');
    expect(model?.value).toBe('saved-model');
  });

  it('Reads the diagnostics pane for the profile the sheet is editing', async () => {
    seedConnection('local');
    vi.spyOn(commands, 'hermesAgentProfiles').mockResolvedValue(ok(AGENT_PROFILES));
    const cliRead = vi.spyOn(commands, 'hermesCliRead').mockResolvedValue(ok('mcp servers: none'));
    const store = useHermesStore();
    await store.loadAgentProfiles();
    wrapper = mount(HermesSettings, {
      props: { open: false, tab: 'tools' },
      attachTo: document.body,
      global: { plugins: [i18n] }
    });
    await wrapper.setProps({ open: true });
    await vi.waitFor(() => expect(commands.hermesModelOptions).toHaveBeenCalled());

    const select = profileScopeSelect();
    expect(select).not.toBeNull();
    select!.value = 'research';
    select!.dispatchEvent(new Event('change'));

    // MCP servers, skills, toolsets and cron jobs are per profile; reading them from the root
    // home listed the default profile's state while another profile was selected.
    await vi.waitFor(() => expect(cliRead).toHaveBeenCalledWith('tools', 'research'));
  });

  it('Shows the shared loading ring while updating the local runtime', async () => {
    seedConnection('local');
    vi.spyOn(commands, 'hermesRuntimeStatus').mockResolvedValue(
      ok({
        ready: true,
        installed: true,
        version: '0.23.0',
        expectedVersion: '0.23.2',
        path: '/tmp/hermes',
        dataPath: '/tmp',
        computerUse: false,
        outdated: true,
        message: null
      })
    );
    let finishInstall!: () => void;
    const install = new Promise<Awaited<ReturnType<typeof commands.hermesRuntimeInstall>>>(resolve => {
      finishInstall = () =>
        resolve(
          ok({
            ready: true,
            installed: true,
            version: '0.23.2',
            expectedVersion: '0.23.2',
            path: '/tmp/hermes',
            dataPath: '/tmp',
            computerUse: false,
            outdated: false,
            message: null
          })
        );
    });
    vi.spyOn(commands, 'hermesRuntimeInstall').mockReturnValue(install);
    wrapper = mount(HermesSettings, {
      props: { open: false, tab: 'mac' },
      attachTo: document.body,
      global: { plugins: [i18n] }
    });
    await wrapper.setProps({ open: true });

    await vi.waitFor(() => expect(sheetText()).toContain('Update local Hermes'));
    const update = Array.from(document.body.querySelectorAll('button')).find(
      button => button.textContent?.trim() === 'Update local Hermes'
    );
    expect(update).toBeDefined();
    update!.click();

    await vi.waitFor(() =>
      expect(document.body.querySelector('button[aria-busy="true"]')?.textContent).toContain('Update local Hermes')
    );
    const busy = document.body.querySelector('button[aria-busy="true"]');
    expect(busy?.hasAttribute('disabled')).toBe(true);
    expect(busy?.querySelector('.on-spinner')).not.toBeNull();

    finishInstall();
  });
});
