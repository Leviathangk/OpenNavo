import { flushPromises, mount } from '@vue/test-utils';
import { NAlert, NButton, NCard, NDrawer, NDrawerContent, NProgress, NSkeleton, NSpin, NTag, NText } from 'naive-ui';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import type { DataOf, Schemas } from '../src/typings/api/opennavo';
import { formatDateTime } from '../src/utils/opennavo';
import SeoCard from '../src/views/dashboard/modules/seo-card.vue';

const api = vi.hoisted(() => ({ overview: vi.fn(), alerts: vi.fn(), geo: vi.fn(), allowed: true }));
vi.mock('@/service/api', () => ({ fetchSeoOverview: api.overview, fetchSeoAlerts: api.alerts, fetchSeoGeo: api.geo }));
vi.mock('@/hooks/business/auth', () => ({
  useAuth: () => ({ hasAuth: (code: string) => api.allowed && code === 'seo:insight:view' })
}));
vi.mock('@/store/modules/app', () => ({ useAppStore: () => ({ locale: 'en-US' }) }));
vi.mock('@/locales', () => ({ $t: (key: string) => key }));
const components = { NAlert, NButton, NCard, NDrawer, NDrawerContent, NProgress, NSkeleton, NSpin, NTag, NText };
const summary = (): DataOf<'getSeoOverview'> => ({
  geo: null,
  keywordCount: 1234,
  targetCount: 42,
  automationReady: true,
  breakers: [],
  budgets: [
    { action: 'listing', period: 'day', used: 3, limit: 20, remaining: 17 },
    { action: 'paid_research', period: 'month', usedMicros: 250001, limitMicros: 15000000, remainingMicros: 14749999 }
  ],
  actionModes: [
    { action: 'listing', enabled: true, mode: 'proposal' },
    { action: 'metadata', enabled: false, mode: 'live' }
  ],
  watermarks: [
    { source: 'gsc_page', completeThrough: '2026-10-16', freshThrough: '2026-10-18', updatedAt: '2026-10-19T00:00:00Z' }
  ],
  yesterday: { actions: [{ operation: 'UpdatePackageMeta', mode: 'live', count: 12, compensated: 1 }] }
});
const issues = (): DataOf<'listSeoAlerts'> => ({
  records: Array.from({ length: 7 }, (_, index) => ({
    id: index,
    kind: 'tech_alert',
    rule: `canonical-${index}`,
    url: `https://fixture.example/apps/${index}`,
    layer: index === 0 ? 'origin' : 'edge',
    severity: 'p1',
    detail: 'Mismatch',
    firstSeen: '2026-10-17T10:00:00Z',
    lastSeen: '2026-10-18T10:00:00Z',
    resolvedAt: index === 6 ? '2026-10-19T10:00:00Z' : null,
    status: index === 6 ? 'resolved' : 'open'
  })),
  current: 1,
  size: 5,
  total: 7
});
beforeEach(() => {
  api.allowed = true;
  api.geo.mockReset().mockResolvedValue({ data: geoDetail() });
  api.overview.mockReset().mockResolvedValue({ data: summary(), error: undefined });
  api.alerts.mockReset().mockResolvedValue({ data: issues(), error: undefined });
});
describe('SEO dashboard card', () => {
  it('Shows counters, exact micro-USD costs, modes, complete watermarks and five open alerts', async () => {
    const wrapper = mount(SeoCard, { global: { components } });
    await flushPromises();
    expect(api.alerts).toHaveBeenCalledWith({ current: 1, size: 5, status: 'open' });
    for (const value of [
      '1,234',
      '42',
      'page.dashboard.seo.ready',
      'page.dashboard.seo.healthy',
      '3 / 20',
      '$0.250001 / $15.00',
      'proposal',
      'live',
      'page.dashboard.seo.disabled',
      '2026-10-16',
      'UpdatePackageMeta',
      'page.dashboard.seo.count 12',
      'page.dashboard.seo.compensated 1',
      'page.dashboard.seo.origin',
      'page.dashboard.seo.edge',
      'page.dashboard.seo.lastSeen',
      'canonical-4',
      'https://fixture.example/apps/0'
    ])
      expect(wrapper.text()).toContain(value);
    expect(wrapper.text()).not.toContain('canonical-5');
    expect(wrapper.text()).not.toContain('canonical-6');
    expect(wrapper.findAllComponents(NProgress)[0]!.props('percentage')).toBe(15);
    wrapper.unmount();
  });
  it('Shows open breaker type, scope, reason and start time', async () => {
    const data = summary();
    data.automationReady = false;
    data.breakers = [
      {
        kind: 'technical',
        scope: '/apps/*',
        reason: 'audit_p0',
        openedAt: '2026-10-18T10:00:00Z',
        retryAt: null,
        evidence: {}
      }
    ];
    api.overview.mockResolvedValue({ data });
    const wrapper = mount(SeoCard, { global: { components } });
    await flushPromises();
    for (const value of [
      'page.dashboard.seo.notReady',
      'page.dashboard.seo.technical',
      '/apps/*',
      'audit_p0',
      'page.dashboard.seo.openedAt',
      '2026-10-18'
    ])
      expect(wrapper.text()).toContain(value);
    expect(wrapper.text()).not.toContain('page.dashboard.seo.healthy');
    wrapper.unmount();
  });
  it.each(['overview', 'alerts'] as const)('Retries a failed %s request', async endpoint => {
    api[endpoint].mockResolvedValueOnce({ error: new Error('Unavailable') });
    const wrapper = mount(SeoCard, { global: { components } });
    await flushPromises();
    expect(wrapper.text()).toContain('page.dashboard.seo.loadFailed');
    await wrapper.find('button').trigger('click');
    await flushPromises();
    expect(wrapper.text()).not.toContain('page.dashboard.seo.loadFailed');
    expect(wrapper.text()).toContain('1,234');
    expect(api[endpoint]).toHaveBeenCalledTimes(2);
    wrapper.unmount();
  });
  it('Handles rejected requests and empty data', async () => {
    api.overview.mockRejectedValueOnce(new Error('Network error')).mockResolvedValue({
      data: { ...summary(), budgets: [], actionModes: [], watermarks: [], yesterday: { actions: [] } }
    });
    api.alerts.mockResolvedValue({ data: { ...issues(), records: [] } });
    const wrapper = mount(SeoCard, { global: { components } });
    await flushPromises();
    expect(wrapper.text()).toContain('page.dashboard.seo.loadFailed');
    await wrapper.find('button').trigger('click');
    await flushPromises();
    for (const value of ['noData', 'noActivity', 'noAlerts'])
      expect(wrapper.text()).toContain(`page.dashboard.seo.${value}`);
    wrapper.unmount();
  });
  it('Renders nothing and makes no requests without permission', async () => {
    api.allowed = false;
    const wrapper = mount(SeoCard, { global: { components } });
    await flushPromises();
    expect(wrapper.findComponent(NCard).exists()).toBe(false);
    expect(wrapper.text()).toBe('');
    expect(api.overview).not.toHaveBeenCalled();
    expect(api.alerts).not.toHaveBeenCalled();
    expect(api.geo).not.toHaveBeenCalled();
    wrapper.unmount();
  });
});

function geoSummary(): NonNullable<Schemas['SeoGeoSummary']> {
  return {
    id: 9,
    site: 'fixture.example',
    mode: 'imported_observation',
    panelId: 'core',
    panelVersion: 'v1',
    periodStart: '2026-10-18T09:00:00Z',
    periodEnd: '2026-10-19T09:00:00Z',
    importedAt: '2026-10-20T09:00:00Z',
    payloadHash: 'hash',
    comparisonKey: 'key',
    supersedes: null,
    trend: null,
    counts: {
      planned: 5,
      answered: 1,
      searchTriggered: { positive: 1, known: 1 },
      mentioned: { positive: 1, known: 1 },
      candidate: { positive: 0, known: 1 },
      cited: { positive: 0, known: 1 },
      citationComplete: 1,
      reported: null
    }
  };
}
function geoDetail(): DataOf<'getSeoGeo'> {
  const geo = geoSummary();
  return {
    ...geo,
    importedBy: 'agent:1',
    snapshot: {
      schemaVersion: 1,
      provider: 'openseo',
      sourceProject: 'fixture',
      providerRunKey: 'run',
      panelId: 'core',
      panelVersion: 'v1',
      approvedBatchRef: 'batch',
      hashNormalization: 'v1',
      mode: geo.mode,
      periodStart: geo.periodStart,
      periodEnd: geo.periodEnd,
      context: { market: 'unknown', promptLanguage: 'en', answerLanguage: 'en', searchMode: 'unknown', windowDays: 1 },
      prompts: [{ id: 'p1', text: 'DO NOT PREVIEW THIS PROMPT', hash: 'hash', classification: 'neutral' }],
      engines: [
        { key: 'chatgpt', name: 'ChatGPT', surface: 'provider_api', modelVersion: 'unknown', toolVersion: 'unknown' }
      ],
      plannedCount: 5,
      cells: (['answered', 'not_triggered', 'error', 'unsupported', 'missing'] as const).map((status, index) => ({
        id: `c${index}`,
        promptId: 'p1',
        engineKey: 'chatgpt',
        status,
        observationId: null,
        answerHash: null,
        searchTriggered: null,
        citationExtraction: 'unknown',
        evidenceAvailability: 'unknown',
        mentioned: null,
        candidate: null,
        cited: null,
        ownUrls: [],
        thirdPartyDomains: [],
        domainsTruncated: false
      })),
      reported: null,
      artifactCompleteness: 'complete',
      hashAlgorithm: 'sha256',
      charges: []
    },
    observationLinks: [
      { observationId: '<b>plain-observation</b>', url: null },
      { observationId: 'linked-observation', url: 'https://evidence.example/observations/linked-observation' }
    ]
  };
}
function mountGeo(geo: Schemas['SeoGeoSummary']) {
  api.overview.mockResolvedValue({ data: { ...summary(), geo } });
  return mount(SeoCard, { attachTo: document.body, global: { components } });
}

describe('GEO summary and read-only details', () => {
  it('Shows not imported without inventing zero counts or a details action', async () => {
    const wrapper = mountGeo(null);
    await flushPromises();
    const section = wrapper.get('[data-testid="geo-section"]');
    expect(section.text()).toContain('page.dashboard.seo.geo.notImported');
    expect(section.text()).not.toContain('0');
    expect(section.find('[data-testid="geo-details"]').exists()).toBe(false);
    expect(api.geo).not.toHaveBeenCalled();
    wrapper.unmount();
  });
  it('Labels summary-only reported values without manufacturing measured denominators', async () => {
    const geo = geoSummary();
    geo.mode = 'summary_only';
    geo.counts.answered = null;
    geo.counts.mentioned = { positive: 0, known: 0 };
    geo.counts.cited = { positive: 0, known: 0 };
    geo.counts.reported = { answered: 49, mentioned: 3, candidate: null, cited: 0, missingFields: ['observations'] };
    const wrapper = mountGeo(geo);
    await flushPromises();
    const section = wrapper.get('[data-testid="geo-section"]');
    for (const value of [
      'summary_only',
      'page.dashboard.seo.geo.reported',
      'page.dashboard.seo.geo.noComparison',
      'observations'
    ])
      expect(section.text()).toContain(value);
    expect(section.get('[data-geo-count="answered"]').text()).toContain('49 / page.dashboard.seo.geo.unknown');
    expect(section.get('[data-geo-count="mentioned"]').text()).toContain('3 / page.dashboard.seo.geo.unknown');
    expect(section.get('[data-geo-count="cited"]').text()).toContain('0 / page.dashboard.seo.geo.unknown');
    expect(section.get('[data-geo-count="candidate"]').text()).toContain(
      'page.dashboard.seo.geo.unknown / page.dashboard.seo.geo.unknown'
    );
    expect(section.text()).not.toContain('%');
    wrapper.unmount();
  });
  it('Shows measured counts and the formatted sampling period', async () => {
    const geo = geoSummary();
    const wrapper = mountGeo(geo);
    await flushPromises();
    const section = wrapper.get('[data-testid="geo-section"]');
    expect(section.get('[data-geo-count="planned"] dd').text()).toBe('5');
    expect(section.get('[data-geo-count="answered"] dd').text()).toBe('1 / 5');
    expect(section.get('[data-geo-count="mentioned"] dd').text()).toBe('1 / 1');
    expect(section.get('[data-geo-count="cited"] dd').text()).toBe('0 / 1');
    expect(section.text()).toContain(formatDateTime(geo.periodStart));
    expect(section.text()).toContain(formatDateTime(geo.periodEnd));
    expect(section.text()).not.toContain('summary_only');
    wrapper.unmount();
  });
  it('Preserves unavailable denominators instead of showing zero percent', async () => {
    const geo = geoSummary();
    geo.counts.mentioned = { positive: 0, known: 0 };
    Reflect.deleteProperty(geo.counts.cited, 'known');
    const wrapper = mountGeo(geo);
    await flushPromises();
    for (const key of ['mentioned', 'cited'])
      expect(wrapper.get(`[data-geo-count="${key}"] dd`).text()).toBe('0 / page.dashboard.seo.geo.unknown');
    expect(wrapper.get('[data-testid="geo-section"]').text()).not.toContain('%');
    wrapper.unmount();
  });
  it('Loads structured details, engine statuses and plain links without rendering HTML or prompt text', async () => {
    const wrapper = mountGeo(geoSummary());
    await flushPromises();
    expect(api.geo).not.toHaveBeenCalled();
    await wrapper.get('[data-testid="geo-details"]').trigger('click');
    await flushPromises();
    expect(api.geo).toHaveBeenCalledWith(9);
    const detail = document.querySelector('[data-testid="geo-detail"]')!;
    expect(detail.textContent).toContain('ChatGPT');
    for (const state of ['answered', 'not_triggered', 'error', 'unsupported', 'missing'])
      expect(detail.textContent).toContain(`page.dashboard.seo.geo.status.${state}: 1`);
    expect(detail.textContent).toContain('<b>plain-observation</b>');
    expect(detail.querySelector('b')).toBeNull();
    expect(detail.textContent).not.toContain('DO NOT PREVIEW THIS PROMPT');
    const links = detail.querySelectorAll('a');
    expect(links).toHaveLength(1);
    expect(links[0]!.getAttribute('href')).toBe('https://evidence.example/observations/linked-observation');
    expect(links[0]!.getAttribute('target')).toBe('_blank');
    expect(links[0]!.getAttribute('rel')).toBe('noopener noreferrer');
    wrapper.unmount();
  });
  it.each(['business', 'network'])('Keeps the summary when detail loading fails (%s) and allows retry', async kind => {
    if (kind === 'business') api.geo.mockResolvedValueOnce({ error: new Error('Unavailable') });
    else api.geo.mockRejectedValueOnce(new Error('Offline'));
    const wrapper = mountGeo(geoSummary());
    await flushPromises();
    await wrapper.get('[data-testid="geo-details"]').trigger('click');
    await flushPromises();
    expect(document.body.textContent).toContain('page.dashboard.seo.geo.loadFailed');
    expect(wrapper.get('[data-geo-count="answered"] dd').text()).toBe('1 / 5');
    (document.querySelector('[data-testid="geo-retry"]') as HTMLButtonElement).click();
    await flushPromises();
    expect(api.geo).toHaveBeenCalledTimes(2);
    expect(document.querySelector('[data-testid="geo-detail"]')).not.toBeNull();
    expect(document.body.textContent).not.toContain('page.dashboard.seo.geo.loadFailed');
    wrapper.unmount();
  });
});
