import { mount } from '@vue/test-utils';
import { describe, expect, it } from 'vitest';
import OnPackageSourceBadge from '../src/components/OnPackageSourceBadge.vue';
import { withLocale } from './helpers';

// The icon used to be read from the translated label: a "✓" anywhere in the text suppressed it. It now
// follows the typed props, so copy can be reworded without changing the icon.
const mountBadge = (props: Record<string, unknown>) =>
  mount(OnPackageSourceBadge, {
    props: {
      label: 'Official source',
      description: 'Source: vendor.example',
      state: 'active',
      verified: true,
      ...props
    },
    global: withLocale('en-US')
  });
const badge = (props: Record<string, unknown>) => {
  const wrapper = mountBadge(props);
  const element = wrapper.get('[data-testid="package-source-badge"]');
  const hasIcon = element.find('svg').exists();
  const state = element.attributes('data-state');
  wrapper.unmount();
  return { hasIcon, state };
};

describe('Source badge icon', () => {
  it('draws the check icon for a verified label that has no check of its own', () => {
    expect(badge({}).hasIcon).toBe(true);
  });

  it('draws no icon when the host says the label carries its own check mark', () => {
    expect(badge({ label: '✓ Official source · Verified', labelHasCheck: true }).hasIcon).toBe(false);
  });

  it('does not read a check mark out of the label', () => {
    // The same copy as the verified string, but the host has not declared it as carrying the mark.
    expect(badge({ label: '✓ Official source · Verified' }).hasIcon).toBe(true);
  });

  it('marks an unverified source with the info icon, whatever the label says', () => {
    const result = badge({ verified: false, label: '✓ Official source' });
    expect(result.hasIcon).toBe(true);
    expect(result.state).toBe('active');
  });

  it.each(['disabled', 'deprecated', 'paused'] as const)('marks a %s source with the alert icon', state => {
    const result = badge({ state, labelHasCheck: true });
    expect(result.hasIcon).toBe(true);
    expect(result.state).toBe(state);
  });
});
