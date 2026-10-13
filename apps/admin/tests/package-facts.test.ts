import { flushPromises, mount } from '@vue/test-utils';
import {
  NAlert,
  NAvatar,
  NButton,
  NCard,
  NCheckbox,
  NDatePicker,
  NDescriptions,
  NDescriptionsItem,
  NEmpty,
  NForm,
  NFormItem,
  NInput,
  NInputNumber,
  NList,
  NListItem,
  NRadio,
  NRadioButton,
  NRadioGroup,
  NSelect,
  NSkeleton,
  NTag,
  NText
} from 'naive-ui';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import FactsTab from '../src/views/catalog/package-detail/modules/facts-tab.vue';
import {
  alternativesBody,
  alternativesValid,
  gapKey,
  macosBody,
  macosForm,
  macosProblem,
  priceBody,
  priceForm,
  priceProblem
} from '../src/views/catalog/package-detail/modules/facts';
import type { AlternativeRow } from '../src/views/catalog/package-detail/modules/facts';

const api = vi.hoisted(() => ({ load: vi.fn(), price: vi.fn(), macos: vi.fn(), alternatives: vi.fn() }));
vi.mock('@/service/api', () => ({
  fetchPackageFacts: api.load,
  updatePackagePrice: api.price,
  updatePackageMacosSupport: api.macos,
  updatePackageAlternatives: api.alternatives
}));
vi.mock('@/locales', () => ({ $t: (key: string) => key }));
vi.mock('@/hooks/business/auth', () => ({ useAuth: () => ({ hasAuth: () => true }) }));
vi.mock('@/hooks/business/localized', () => ({
  useLocalizedPackage: () => ({ nameOf: (row: { name: string }) => row.name })
}));

const components = {
  NAlert,
  NAvatar,
  NButton,
  NCard,
  NCheckbox,
  NDatePicker,
  NDescriptions,
  NDescriptionsItem,
  NEmpty,
  NForm,
  NFormItem,
  NInput,
  NInputNumber,
  NList,
  NListItem,
  NRadio,
  NRadioButton,
  NRadioGroup,
  NSelect,
  NSkeleton,
  NTag,
  NText
};

const evidence = {
  appVersion: '5.2',
  sourceUrl: 'https://vendor.example/pricing',
  method: 'vendor_site' as const,
  verifiedAt: '2026-10-13T08:00:00Z',
  verifiedBy: 'Hermes',
  verifier: 'agent' as const,
  status: 'verified' as const,
  note: null,
  stale: false
};

function facts(overrides: Record<string, unknown> = {}) {
  return {
    packageId: 7,
    price: null,
    macos: {
      ...evidence,
      ranges: [{ arch: 'arm64' as const, support: 'native' as const, minimum: '12', maximum: null }]
    },
    homebrew: {
      supportStatus: 'known',
      arm64: true,
      x86_64: true,
      requiresRosetta: false,
      minimum: '13',
      maximum: null
    },
    conflicts: ['minimum'],
    alternatives: [
      {
        packageId: 9,
        token: 'hidden-editor',
        name: 'Hidden Editor',
        displayName: 'Hidden Editor',
        iconUrl: null,
        reasons: ['privacy'],
        limits: [],
        bestFor: null,
        note: null,
        source: 'agent',
        available: false
      }
    ],
    offerReady: false,
    gaps: ['price.unknown', 'macos.conflict', 'alternatives.unavailable'],
    updatedAt: '2026-10-13T08:00:00Z',
    ...overrides
  };
}

beforeEach(() => {
  api.load.mockReset().mockResolvedValue({ data: facts(), error: undefined });
  for (const call of [api.price, api.macos, api.alternatives])
    call.mockReset().mockResolvedValue({ data: facts({ gaps: [] }), error: undefined });
});

describe('Facts forms', () => {
  it('Builds price bodies that never claim more than the evidence', () => {
    const form = priceForm(null);
    expect(priceBody(form)).toEqual({ type: 'unknown' });
    form.type = 'subscription';
    expect(priceProblem(form)).toBe('sourceRequired');
    form.evidence.sourceUrl = ' https://vendor.example/pricing ';
    form.evidence.method = 'vendor_site';
    expect(priceProblem(form)).toBeNull();
    form.amount = 9.99;
    expect(priceProblem(form)).toBe('amountInvalid');
    form.currency = 'USD';
    form.period = 'one_time';
    expect(priceProblem(form)).toBe('amountInvalid');
    form.period = 'month';
    form.region = 'usa';
    expect(priceProblem(form)).toBe('amountInvalid');
    form.region = 'US';
    expect(priceProblem(form)).toBeNull();
    expect(priceBody(form)).toMatchObject({
      type: 'subscription',
      amount: 9.99,
      currency: 'USD',
      period: 'month',
      region: 'US',
      plan: null,
      sourceUrl: 'https://vendor.example/pricing',
      verifiedAt: null
    });
    // Free has no amount even if one was typed before switching.
    form.type = 'free';
    expect(priceBody(form)).toMatchObject({ type: 'free', amount: null, currency: null, period: null, region: null });
    form.type = 'paid';
    form.amount = 1.234;
    expect(priceProblem(form)).toBe('amountInvalid');
  });

  it('Keeps versions and architectures valid in the macOS supplement', () => {
    const form = macosForm(null);
    expect(macosProblem(form)).toBe('rangesRequired');
    form.ranges.arm64.include = true;
    form.ranges.arm64.minimum = '14';
    form.ranges.arm64.maximum = '13.6';
    expect(macosProblem(form)).toBe('rangesRequired');
    form.ranges.arm64.maximum = '';
    expect(macosProblem(form)).toBe('sourceRequired');
    form.evidence.sourceUrl = 'https://vendor.example/requirements';
    form.evidence.method = 'vendor_docs';
    form.ranges.x86_64 = { include: true, support: 'unsupported', minimum: '12', maximum: '' };
    expect(macosProblem(form)).toBeNull();
    expect(macosBody(form).ranges).toEqual([
      { arch: 'arm64', support: 'native', minimum: '14', maximum: null },
      { arch: 'x86_64', support: 'unsupported', minimum: null, maximum: null }
    ]);
    form.ranges.x86_64.support = 'rosetta';
    expect(macosProblem(form)).toBe('rangesRequired');
  });

  it('Requires reasons for alternatives and maps gap codes to messages', () => {
    const row = facts().alternatives[0] as AlternativeRow;
    expect(alternativesValid([row])).toBe(true);
    expect(alternativesValid([{ ...row, reasons: [] }])).toBe(false);
    expect(alternativesBody([{ ...row, note: '  ' }])).toEqual({
      items: [{ packageId: 9, reasons: ['privacy'], limits: [], bestFor: null, note: null }]
    });
    expect(gapKey('alternatives.unavailable')).toBe('alternativesUnavailable');
  });
});

describe('Facts tab', () => {
  it('Shows gaps, Homebrew’s side and conflicts, and checks evidence before saving a price', async () => {
    const wrapper = mount(FactsTab, {
      props: { packageId: 7 },
      global: { components, stubs: { PackagePicker: true } }
    });
    await flushPromises();
    expect(wrapper.find('[data-testid="facts-gaps"]').text()).toContain(
      'page.catalog.packageDetail.facts.gaps.macosConflict'
    );
    expect(wrapper.find('[data-testid="facts-conflicts"]').exists()).toBe(true);
    expect(wrapper.find('[data-testid="facts-alternatives"]').text()).toContain(
      'page.catalog.packageDetail.facts.hidden'
    );

    const price = wrapper.find('[data-testid="facts-price"]');
    const free = price
      .findAll('input[type="radio"]')
      .find(node => (node.element as HTMLInputElement).value === 'free')!;
    await free.setValue(true);
    const save = () => price.findAll('button').find(node => node.text() === 'page.catalog.packageDetail.facts.save')!;
    await save().trigger('click');
    await flushPromises();
    expect(api.price).not.toHaveBeenCalled();
    expect(price.text()).toContain('page.catalog.packageDetail.facts.sourceRequired');
  });

  it.each(['price', 'macos', 'alternatives'])(
    'Saving %s preserves other drafts and validation errors',
    async section => {
      const wrapper = mount(FactsTab, {
        props: { packageId: 7 },
        global: { components, stubs: { PackagePicker: true } }
      });
      await flushPromises();
      const minimum = wrapper.find('[data-testid="facts-macos"] input[aria-label$=".minimum"]');
      await minimum.setValue('14');
      const note = wrapper.find('[data-testid="facts-alternatives"] input');
      await note.setValue('Unsaved note');
      const price = wrapper.find('[data-testid="facts-price"]');
      if (section !== 'price') {
        await price
          .findAll('input[type="radio"]')
          .find(node => (node.element as HTMLInputElement).value === 'free')!
          .setValue(true);
        await price
          .findAll('button')
          .find(node => node.text() === 'page.catalog.packageDetail.facts.save')!
          .trigger('click');
      }
      await wrapper
        .find(`[data-testid="facts-${section}"]`)
        .findAll('button')
        .find(node => node.text() === 'page.catalog.packageDetail.facts.save')!
        .trigger('click');
      await flushPromises();
      expect(api[section as keyof typeof api]).toHaveBeenCalled();
      expect((minimum.element as HTMLInputElement).value).toBe(section === 'macos' ? '12' : '14');
      expect((note.element as HTMLInputElement).value).toBe(section === 'alternatives' ? '' : 'Unsaved note');
      if (section !== 'price') {
        expect(price.text()).toContain('page.catalog.packageDetail.facts.sourceRequired');
        expect(
          price.findAll('input[type="radio"]').find(node => (node.element as HTMLInputElement).value === 'free')!
            .element
        ).toHaveProperty('checked', true);
      }
      wrapper.unmount();
    }
  );

  it.each([false, true])('Locks alternatives until a pending save settles (error=%s)', async failed => {
    let finish!: (value: unknown) => void;
    api.alternatives.mockImplementation(
      () =>
        new Promise(resolve => {
          finish = resolve;
        })
    );
    const wrapper = mount(FactsTab, {
      props: { packageId: 7 },
      global: {
        components,
        stubs: {
          PackagePicker: {
            props: ['disabled'],
            template: '<button data-testid="picker" :disabled="disabled">Pick</button>'
          }
        }
      }
    });
    await flushPromises();
    const card = wrapper.find('[data-testid="facts-alternatives"]');
    const note = card.find('input');
    await note.setValue('Submitted note');
    await card
      .findAll('button')
      .find(node => node.text() === 'page.catalog.packageDetail.facts.save')!
      .trigger('click');
    await flushPromises();
    expect((note.element as HTMLInputElement).disabled).toBe(true);
    for (const select of wrapper.findAllComponents(NSelect).filter(node => card.element.contains(node.element))) {
      expect(select.props('disabled')).toBe(true);
    }
    for (const button of card
      .findAll('button')
      .filter(node => node.text() !== 'page.catalog.packageDetail.facts.save')) {
      expect((button.element as HTMLButtonElement).disabled).toBe(true);
    }
    const minimum = wrapper.find('[data-testid="facts-macos"] input[aria-label$=".minimum"]');
    expect((minimum.element as HTMLInputElement).disabled).toBe(false);
    await minimum.setValue('14');
    finish(failed ? { error: true } : { data: facts(), error: undefined });
    await flushPromises();
    expect((note.element as HTMLInputElement).disabled).toBe(false);
    expect((minimum.element as HTMLInputElement).value).toBe('14');
    if (failed) expect((note.element as HTMLInputElement).value).toBe('Submitted note');
    expect((card.find('[data-testid="picker"]').element as HTMLButtonElement).disabled).toBe(false);
    wrapper.unmount();
  });

  it('Saves alternatives and reloads the returned facts', async () => {
    const wrapper = mount(FactsTab, {
      props: { packageId: 7 },
      global: { components, stubs: { PackagePicker: true } }
    });
    await flushPromises();
    const card = wrapper.find('[data-testid="facts-alternatives"]');
    await card
      .findAll('button')
      .find(node => node.text() === 'page.catalog.packageDetail.facts.save')!
      .trigger('click');
    await flushPromises();
    expect(api.alternatives).toHaveBeenCalledWith(7, {
      items: [{ packageId: 9, reasons: ['privacy'], limits: [], bestFor: null, note: null }]
    });
    expect(wrapper.find('[data-testid="facts-gaps"]').text()).toContain('page.catalog.packageDetail.facts.noGaps');
  });
});
