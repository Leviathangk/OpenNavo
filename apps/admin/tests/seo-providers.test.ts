import { flushPromises, mount } from '@vue/test-utils';
import {
  NAlert,
  NButton,
  NCard,
  NDivider,
  NForm,
  NFormItem,
  NInput,
  NP,
  NRadioButton,
  NRadioGroup,
  NSkeleton,
  NSwitch,
  NTag,
  NText
} from 'naive-ui';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import DataProviders from '../src/views/system/seo/modules/data-providers.vue';
import { serviceAccountProblem, validGscProperty, validOrigin } from '../src/views/system/seo/modules/validate';

const api = vi.hoisted(() => ({ load: vi.fn(), save: vi.fn() }));
vi.mock('@/service/api', () => ({ fetchSeoProvider: api.load, updateSeoProvider: api.save }));
vi.mock('@/locales', () => ({ $t: (key: string) => key }));

type Source = 'gsc' | 'bing' | 'audit';
const TYPES = { gsc: 'service_account', bing: 'api_key', audit: 'origin' } as const;
const MARKS = {
  gsc: ['gsc_page', 'gsc_query', 'inspection'],
  bing: ['bing'],
  audit: ['audit', 'edge_probe']
} as const;

// Fictional key: the shape check only needs the markers, never a real private key.
const KEY = JSON.stringify({
  type: 'service_account',
  client_email: 'reader@fixture-project.iam.gserviceaccount.com',
  private_key: '-----BEGIN PRIVATE KEY-----\nfixture\n-----END PRIVATE KEY-----\n'
});

function view(source: Source, configured = false, enabled = false) {
  return {
    source,
    enabled,
    configured,
    credentialType: configured ? TYPES[source] : null,
    watermarks: MARKS[source].map((mark, index) => ({
      source: mark,
      completeThrough: index === 0 && configured ? '2026-10-20' : null,
      freshThrough: index === 0 && configured ? '2026-10-22' : null,
      updatedAt: '2026-10-23T00:00:00Z'
    }))
  };
}

function page() {
  return mount(DataProviders, {
    props: { siteUrl: 'https://opennavo.example', revision: 0 },
    global: {
      components: {
        NAlert,
        NButton,
        NCard,
        NDivider,
        NForm,
        NFormItem,
        NInput,
        NP,
        NRadioButton,
        NRadioGroup,
        NSkeleton,
        NSwitch,
        NTag,
        NText
      }
    }
  });
}

const prefix = 'page.system.seo.providers.';
const section = (wrapper: ReturnType<typeof page>, source: Source) =>
  wrapper.find(`[data-testid="provider-${source}"]`);
const save = (wrapper: ReturnType<typeof page>, source: Source) =>
  wrapper.find(`[data-testid="save-${source}"]`).trigger('click');

beforeEach(() => {
  api.load.mockReset().mockImplementation(async (source: Source) => ({ data: view(source), error: undefined }));
  api.save
    .mockReset()
    .mockImplementation(async (source: Source) => ({ data: view(source, true, true), error: undefined }));
});

describe('SEO data providers', () => {
  it('Shows each source with its state, credential kind and data progress', async () => {
    api.load.mockImplementation(async (source: Source) => ({
      data: view(source, source === 'gsc', source === 'gsc'),
      error: undefined
    }));
    const wrapper = page();
    await flushPromises();
    expect(api.load).toHaveBeenCalledTimes(3);
    const gsc = section(wrapper, 'gsc').text();
    expect(gsc).toContain(`${prefix}state.enabled`);
    expect(gsc).toContain(`${prefix}credentialTypes.service_account`);
    expect(wrapper.find('[data-testid="progress-gsc"]').findAll('span').length).toBeGreaterThanOrEqual(3);
    expect(section(wrapper, 'bing').text()).toContain(`${prefix}state.notConfigured`);
    wrapper.unmount();
  });

  it('Sends a pasted service account key with the property and clears it after saving', async () => {
    const wrapper = page();
    await flushPromises();
    const gsc = section(wrapper, 'gsc');
    await gsc
      .find('input[aria-label="page.system.seo.providers.sources.gsc.property"]')
      .setValue('sc-domain:opennavo.example');
    await gsc
      .findAll('button')
      .find(button => button.text() === `${prefix}sources.gsc.pasteJson`)!
      .trigger('click');
    await gsc.find('textarea').setValue(KEY);
    await save(wrapper, 'gsc');
    await flushPromises();
    expect(api.save).toHaveBeenCalledWith('gsc', {
      enabled: false,
      credentials: { property: 'sc-domain:opennavo.example', serviceAccountJson: KEY }
    });
    expect(section(wrapper, 'gsc').find('textarea').exists()).toBe(false);
    expect(wrapper.html()).not.toContain('PRIVATE KEY');
    wrapper.unmount();
  });

  it('Blocks an invalid key or property before any request', async () => {
    const wrapper = page();
    await flushPromises();
    const gsc = section(wrapper, 'gsc');
    await gsc.find('input[aria-label="page.system.seo.providers.sources.gsc.property"]').setValue('opennavo.example');
    await save(wrapper, 'gsc');
    await flushPromises();
    expect(wrapper.find('[data-testid="problem-gsc"]').text()).toBe(`${prefix}errors.gscProperty`);
    await gsc
      .find('input[aria-label="page.system.seo.providers.sources.gsc.property"]')
      .setValue('sc-domain:opennavo.example');
    await save(wrapper, 'gsc');
    await flushPromises();
    expect(wrapper.find('[data-testid="problem-gsc"]').text()).toBe(`${prefix}errors.serviceAccount.missing`);
    await gsc
      .findAll('button')
      .find(button => button.text() === `${prefix}sources.gsc.pasteJson`)!
      .trigger('click');
    await gsc.find('textarea').setValue('{"type":"authorized_user"}');
    await save(wrapper, 'gsc');
    await flushPromises();
    expect(wrapper.find('[data-testid="problem-gsc"]').text()).toBe(`${prefix}errors.serviceAccount.notServiceAccount`);
    expect(api.save).not.toHaveBeenCalled();
    wrapper.unmount();
  });

  it('Requires credentials before enabling an unconfigured source', async () => {
    const wrapper = page();
    await flushPromises();
    await section(wrapper, 'bing').find('[role="switch"]').trigger('click');
    await save(wrapper, 'bing');
    await flushPromises();
    expect(wrapper.find('[data-testid="problem-bing"]').text()).toBe(`${prefix}errors.credentialsRequired`);
    expect(api.save).not.toHaveBeenCalled();
    wrapper.unmount();
  });

  it('Clearing stored credentials also disables the source', async () => {
    api.load.mockImplementation(async (source: Source) => ({ data: view(source, true, true), error: undefined }));
    api.save.mockImplementation(async (source: Source) => ({ data: view(source), error: undefined }));
    const wrapper = page();
    await flushPromises();
    const switches = section(wrapper, 'audit').findAll('[role="switch"]');
    await switches[switches.length - 1]!.trigger('click');
    await save(wrapper, 'audit');
    await flushPromises();
    expect(api.save).toHaveBeenCalledWith('audit', { enabled: false, clearCredentials: true });
    expect(section(wrapper, 'audit').text()).toContain(`${prefix}state.notConfigured`);
    wrapper.unmount();
  });

  it.each(['gsc', 'bing', 'audit'] as const)('Disables %s edits during save and reload', async source => {
    api.load.mockImplementation(async (key: Source) => ({ data: view(key, true), error: undefined }));
    const wrapper = page();
    await flushPromises();
    const provider = section(wrapper, source);
    await provider.find('[role="switch"]').trigger('click');
    let finish!: () => void;
    api.save.mockImplementation(
      () =>
        new Promise(resolve => {
          finish = () => resolve({ data: view(source, true, true), error: undefined });
        })
    );
    await save(wrapper, source);
    expect(api.save).toHaveBeenCalledWith(source, { enabled: true });
    const expectDisabled = () => {
      const controls = provider.findAll('input, textarea, button');
      expect(controls.length).toBeGreaterThan(0);
      for (const control of controls) expect(control.attributes('disabled')).toBeDefined();
    };
    expectDisabled();
    await provider.find('[role="switch"]').trigger('click');
    expect(provider.find('[role="switch"]').attributes('aria-checked')).toBe('true');
    finish();
    await flushPromises();
    expect(provider.find('input').attributes('disabled')).toBeUndefined();
    api.load.mockImplementation((key: Source) =>
      key === source
        ? new Promise(resolve => {
            finish = () => resolve({ data: view(source, true, true), error: undefined });
          })
        : Promise.resolve({ data: view(key, true), error: undefined })
    );
    await wrapper.setProps({ revision: 1 });
    await flushPromises();
    expectDisabled();
    finish();
    await flushPromises();
    expect(provider.find('input').attributes('disabled')).toBeUndefined();
    wrapper.unmount();
  });

  it('Keeps other sources usable when one fails to load', async () => {
    api.load.mockImplementation(async (source: Source) =>
      source === 'bing' ? { data: null, error: new Error('offline') } : { data: view(source), error: undefined }
    );
    const wrapper = page();
    await flushPromises();
    expect(section(wrapper, 'bing').text()).toContain(`${prefix}loadFailed`);
    expect(wrapper.find('[data-testid="save-gsc"]').exists()).toBe(true);
    expect(wrapper.find('[data-testid="save-bing"]').exists()).toBe(false);
    wrapper.unmount();
  });
});

describe('Provider credential checks', () => {
  it('Accepts domain and URL-prefix properties only', () => {
    expect(validGscProperty('sc-domain:opennavo.example')).toBe(true);
    expect(validGscProperty('https://opennavo.example/')).toBe(true);
    expect(validGscProperty('opennavo.example')).toBe(false);
    expect(validGscProperty('sc-domain:')).toBe(false);
  });

  it('Names what is wrong with a service account key', () => {
    expect(serviceAccountProblem(KEY)).toBeNull();
    expect(serviceAccountProblem('not json')).toBe('invalidJson');
    expect(serviceAccountProblem('[]')).toBe('invalidJson');
    expect(serviceAccountProblem('{"type":"authorized_user"}')).toBe('notServiceAccount');
    expect(serviceAccountProblem('{"type":"service_account","client_email":"x@y.z"}')).toBe('missingFields');
    expect(serviceAccountProblem('x'.repeat(16385))).toBe('tooLong');
  });

  it('Rejects audit origins that are not a bare https host or equal the public host', () => {
    expect(validOrigin('https://origin.opennavo.example', 'https://opennavo.example')).toBe(true);
    expect(validOrigin('https://opennavo.example', 'https://opennavo.example')).toBe(false);
    expect(validOrigin('https://origin.opennavo.example/path', 'https://opennavo.example')).toBe(false);
    expect(validOrigin('http://origin.opennavo.example', 'https://opennavo.example')).toBe(false);
  });
});
