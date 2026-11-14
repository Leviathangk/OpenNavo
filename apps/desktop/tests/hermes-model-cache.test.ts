import { createPinia, setActivePinia } from 'pinia';
import { nextTick } from 'vue';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { commands } from '@/ipc/client';
import { useHermesStore } from '@/stores/hermes';

const ok = <T>(data: T) => ({ status: 'ok' as const, data });
const PROFILES_KEY = 'ONV_HERMES_PROFILES_V1';
const CACHE_PREFIX = 'ONV_HERMES_MODEL_OPTIONS_V3';

const CATALOG = {
  providers: [
    {
      slug: 'nous',
      name: 'Nous',
      is_current: true,
      models: ['Hermes 4 · 405B'],
      total_models: 1
    }
  ],
  model: 'Hermes 4 · 405B',
  provider: 'nous'
};

function seedProfiles(activeProfileId = 'panel-1') {
  localStorage.setItem(
    PROFILES_KEY,
    JSON.stringify({
      version: 1,
      activeProfileId,
      profiles: [
        { id: 'local', name: 'Local', kind: 'local', endpoint: '' },
        { id: 'panel-1', name: 'Panel 1', kind: 'panel', endpoint: 'http://panel-1.test:9119' },
        { id: 'panel-2', name: 'Panel 2', kind: 'panel', endpoint: 'http://panel-2.test:9119' }
      ],
      activeAgentProfiles: { 'panel-1': 'default', 'panel-2': 'default' }
    })
  );
}

function cacheKeys(connectionId: string): string[] {
  const prefix = `${CACHE_PREFIX}::${encodeURIComponent(connectionId)}::`;
  const keys: string[] = [];
  for (let index = 0; index < localStorage.length; index += 1) {
    const key = localStorage.key(index);
    if (key?.startsWith(prefix)) keys.push(key);
  }
  return keys;
}

function cacheKeysForProfile(connectionId: string, agentProfile: string): string[] {
  const prefix = `${CACHE_PREFIX}::${encodeURIComponent(connectionId)}::${encodeURIComponent(agentProfile)}::`;
  const keys: string[] = [];
  for (let index = 0; index < localStorage.length; index += 1) {
    const key = localStorage.key(index);
    if (key?.startsWith(prefix)) keys.push(key);
  }
  return keys;
}

describe('Hermes model catalog cache', () => {
  beforeEach(() => {
    localStorage.clear();
    setActivePinia(createPinia());
    Object.defineProperty(window, '__TAURI_INTERNALS__', { value: {}, configurable: true });
    vi.spyOn(commands, 'hermesModelOptions').mockResolvedValue(ok(CATALOG));
    vi.spyOn(commands, 'hermesPanelLogout').mockResolvedValue(ok(null));
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  it('reuses a fresh cache for the same connection and profile', async () => {
    seedProfiles();
    const store = useHermesStore();

    await store.loadModelOptions(false);
    expect(commands.hermesModelOptions).toHaveBeenCalledTimes(1);
    expect(cacheKeys('panel-1')).toHaveLength(1);

    await store.loadModelOptions(false);
    expect(commands.hermesModelOptions).toHaveBeenCalledTimes(1);

    setActivePinia(createPinia());
    const restored = useHermesStore();
    expect(restored.modelOptions?.providers?.[0]?.slug).toBe('nous');
    expect(restored.modelOptionsUpdatedAt).toBeGreaterThan(0);
  });

  it('does not hydrate another connection cache', async () => {
    seedProfiles('panel-1');
    const store = useHermesStore();
    await store.loadModelOptions(false);

    const persisted = JSON.parse(localStorage.getItem(PROFILES_KEY) ?? '{}') as {
      activeProfileId?: string;
    };
    persisted.activeProfileId = 'panel-2';
    localStorage.setItem(PROFILES_KEY, JSON.stringify(persisted));

    setActivePinia(createPinia());
    const other = useHermesStore();
    expect(other.modelOptions).toBeNull();
    expect(cacheKeys('panel-2')).toHaveLength(0);
  });

  it('does not hydrate another agent profile cache', async () => {
    seedProfiles('panel-1');
    const store = useHermesStore();
    await store.loadModelOptions(false);

    const persisted = JSON.parse(localStorage.getItem(PROFILES_KEY) ?? '{}') as {
      activeAgentProfiles?: Record<string, string>;
    };
    persisted.activeAgentProfiles = { 'panel-1': 'research' };
    localStorage.setItem(PROFILES_KEY, JSON.stringify(persisted));

    setActivePinia(createPinia());
    const other = useHermesStore();
    expect(other.modelOptions).toBeNull();
  });

  it('only exposes the catalog for the scope that owns it', async () => {
    seedProfiles();
    const store = useHermesStore();
    await store.loadModelOptions(false);

    expect(store.modelOptionsForScope('default', store.model)).not.toBeNull();
    expect(store.modelOptionsForScope('research', store.model)).toBeNull();

    await store.loadModelOptions(false, store.model, undefined, 'research');

    expect(store.modelOptionsForScope('default', store.model)).toBeNull();
    expect(store.modelOptionsForScope('research', store.model)).not.toBeNull();
  });

  it('keeps a stale catalog visible while it revalidates', async () => {
    type ModelResult = Awaited<ReturnType<typeof commands.hermesModelOptions>>;
    seedProfiles();
    const store = useHermesStore();
    await store.loadModelOptions(false);

    vi.spyOn(Date, 'now').mockReturnValue(Date.now() + 16 * 60_000);
    let release!: (value: ModelResult) => void;
    const pending = new Promise<ModelResult>(resolve => {
      release = resolve;
    });
    vi.mocked(commands.hermesModelOptions).mockImplementationOnce(() => pending);

    const refresh = store.loadModelOptions(false);
    expect(store.modelOptions?.providers?.[0]?.slug).toBe('nous');
    expect(store.modelOptionsLoading).toBe(true);

    release(
      ok({
        providers: [
          {
            slug: 'anthropic',
            name: 'Anthropic',
            is_current: true,
            models: ['claude-sonnet'],
            total_models: 1
          }
        ],
        model: 'claude-sonnet',
        provider: 'anthropic'
      })
    );
    await refresh;

    expect(commands.hermesModelOptions).toHaveBeenCalledTimes(2);
    expect(store.modelOptions?.providers?.[0]?.slug).toBe('anthropic');
    expect(store.modelOptionsLoading).toBe(false);
  });

  it('clears a loading request when a fresh cache answers for another profile', async () => {
    type ModelResult = Awaited<ReturnType<typeof commands.hermesModelOptions>>;
    seedProfiles();
    const store = useHermesStore();
    await store.loadModelOptions(false, store.model, undefined, 'research');

    let release!: (value: ModelResult) => void;
    const pending = new Promise<ModelResult>(resolve => {
      release = resolve;
    });
    vi.mocked(commands.hermesModelOptions).mockImplementationOnce(() => pending);

    const loadingDefault = store.loadModelOptions(false, store.model, undefined, 'default');
    expect(store.modelOptionsLoading).toBe(true);

    await store.loadModelOptions(false, store.model, undefined, 'research');
    expect(store.modelOptionsLoading).toBe(false);

    release(ok(CATALOG));
    await loadingDefault;
    expect(store.modelOptionsLoading).toBe(false);
  });

  it('does not restore a catalog that was invalidated while its request was in flight', async () => {
    type ModelResult = Awaited<ReturnType<typeof commands.hermesModelOptions>>;
    seedProfiles();
    const store = useHermesStore();
    let release!: (value: ModelResult) => void;
    const pending = new Promise<ModelResult>(resolve => {
      release = resolve;
    });
    vi.mocked(commands.hermesModelOptions).mockImplementationOnce(() => pending);

    const loading = store.loadModelOptions(false);
    expect(commands.hermesModelOptions).toHaveBeenCalledTimes(1);
    store.invalidateModelOptionsCache('default');
    release(ok(CATALOG));
    await loading;

    expect(store.modelOptions).toBeNull();
    expect(cacheKeys('panel-1')).toHaveLength(0);
    expect(store.modelOptionsLoading).toBe(false);
  });

  it('does not restore a catalog after the connection endpoint changes in flight', async () => {
    type ModelResult = Awaited<ReturnType<typeof commands.hermesModelOptions>>;
    seedProfiles();
    const store = useHermesStore();
    let release!: (value: ModelResult) => void;
    const pending = new Promise<ModelResult>(resolve => {
      release = resolve;
    });
    vi.mocked(commands.hermesModelOptions).mockImplementationOnce(() => pending);

    const loading = store.loadModelOptions(false);
    expect(commands.hermesModelOptions).toHaveBeenCalledTimes(1);
    store.updateProfile('panel-1', { endpoint: 'http://panel-1-new.test:9119' });
    release(ok(CATALOG));
    await loading;

    expect(store.modelOptions).toBeNull();
    expect(cacheKeys('panel-1')).toHaveLength(0);
  });

  it('does not restore a profile catalog after that profile is renamed in flight', async () => {
    type ModelResult = Awaited<ReturnType<typeof commands.hermesModelOptions>>;
    seedProfiles();
    vi.spyOn(commands, 'hermesAgentProfileRename').mockResolvedValue(ok(null));
    vi.spyOn(commands, 'hermesAgentProfiles').mockResolvedValue(ok([]));
    const store = useHermesStore();
    let release!: (value: ModelResult) => void;
    const pending = new Promise<ModelResult>(resolve => {
      release = resolve;
    });
    vi.mocked(commands.hermesModelOptions).mockImplementationOnce(() => pending);

    const loading = store.loadModelOptions(false, store.model, undefined, 'research');
    expect(commands.hermesModelOptions).toHaveBeenCalledTimes(1);
    await store.renameAgentProfile('research', 'literature');
    release(ok(CATALOG));
    await loading;

    expect(store.modelOptions).toBeNull();
    expect(cacheKeysForProfile('panel-1', 'research')).toHaveLength(0);
  });

  it('does not rewrite a saved model from a fresh cached catalog on profile switch', async () => {
    seedProfiles('panel-2');
    const store = useHermesStore();
    await store.loadModelOptions(false);
    expect(cacheKeys('panel-2')).toHaveLength(1);

    store.saveModel({ ...store.model, provider: 'custom', model: 'saved-model' });
    await nextTick();

    store.switchProfile('panel-1');
    store.switchProfile('panel-2');
    await nextTick();

    expect(store.model).toMatchObject({ provider: 'custom', model: 'saved-model' });
    expect(store.modelOptions?.providers?.[0]?.slug).toBe('nous');
    const persisted = JSON.parse(localStorage.getItem(PROFILES_KEY) ?? '{}') as {
      states?: Record<string, { model?: { model?: string; provider?: string } }>;
    };
    expect(persisted.states?.['panel-2::default']?.model).toMatchObject({
      provider: 'custom',
      model: 'saved-model'
    });
  });

  it('drops the cache for a removed connection', async () => {
    seedProfiles();
    const store = useHermesStore();
    await store.loadModelOptions(false);
    expect(cacheKeys('panel-1')).toHaveLength(1);

    await store.removeProfile('panel-1');

    expect(cacheKeys('panel-1')).toHaveLength(0);
  });

  it('drops the cache when a connection endpoint changes', async () => {
    seedProfiles();
    const store = useHermesStore();
    await store.loadModelOptions(false);

    store.updateProfile('panel-1', { endpoint: 'http://panel-1-new.test:9119' });

    expect(cacheKeys('panel-1')).toHaveLength(0);
  });

  it('keeps other catalogs when a cache write fails for a non-quota reason', async () => {
    seedProfiles();
    const store = useHermesStore();
    await store.loadModelOptions(false);
    expect(cacheKeysForProfile('panel-1', 'default')).toHaveLength(1);

    const setItem = vi.spyOn(Storage.prototype, 'setItem').mockImplementation(() => {
      throw new Error('storage disabled');
    });
    vi.spyOn(console, 'warn').mockImplementation(() => undefined);

    await store.loadModelOptions(false, store.model, undefined, 'research');

    expect(cacheKeysForProfile('panel-1', 'default')).toHaveLength(1);
    expect(cacheKeysForProfile('panel-1', 'research')).toHaveLength(0);
    setItem.mockRestore();
  });

  it('evicts only the oldest catalog when the storage quota is full', async () => {
    seedProfiles();
    const now = vi.spyOn(Date, 'now');
    const store = useHermesStore();
    now.mockReturnValue(1_000_000);
    await store.loadModelOptions(false);
    now.mockReturnValue(1_000_001);
    await store.loadModelOptions(false, store.model, undefined, 'research');
    expect(cacheKeys('panel-1')).toHaveLength(2);

    const originalSetItem = Storage.prototype.setItem;
    let quotaFailures = 1;
    vi.spyOn(Storage.prototype, 'setItem').mockImplementation(function (this: Storage, key: string, value: string) {
      if (quotaFailures > 0) {
        quotaFailures -= 1;
        const error = new Error('quota');
        error.name = 'QuotaExceededError';
        throw error;
      }
      originalSetItem.call(this, key, value);
    });
    now.mockReturnValue(1_000_002);

    await store.loadModelOptions(false, store.model, undefined, 'third');

    expect(cacheKeysForProfile('panel-1', 'default')).toHaveLength(0);
    expect(cacheKeysForProfile('panel-1', 'research')).toHaveLength(1);
    expect(cacheKeysForProfile('panel-1', 'third')).toHaveLength(1);
  });

  it('can invalidate one agent profile without dropping the other', async () => {
    seedProfiles();
    const store = useHermesStore();
    await store.loadModelOptions(false);
    await store.loadModelOptions(false, store.model, undefined, 'research');
    expect(cacheKeys('panel-1')).toHaveLength(2);

    store.invalidateModelOptionsCache('research');

    expect(cacheKeys('panel-1')).toHaveLength(1);
  });

  it('removes the legacy global cache', () => {
    localStorage.setItem('ONV_HERMES_MODEL_OPTIONS_V2', '{}');
    seedProfiles();

    useHermesStore();

    expect(localStorage.getItem('ONV_HERMES_MODEL_OPTIONS_V2')).toBeNull();
  });
});
