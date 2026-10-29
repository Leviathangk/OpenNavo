import { mount } from '@vue/test-utils';
import { nextTick } from 'vue';
import { describe, expect, it } from 'vitest';
import OnPackageSpecCard from '../src/components/OnPackageSpecCard.vue';
import OnPackageOverview from '../src/components/OnPackageOverview.vue';
import { withLocale } from './helpers';

describe('Package detail product decisions', () => {
  it('keeps one overall date and puts the repository date in an accessible, tappable star tooltip', async () => {
    const wrapper = mount(OnPackageSpecCard, {
      props: {
        title: 'At a glance',
        checkedLabel: 'Checked',
        checkedAt: { iso: '2026-10-01', label: 'October 1, 2026' },
        rows: [
          {
            key: 'repository',
            label: 'Repository',
            value: 'Open source · MIT',
            stars: {
              label: '★12.3K',
              href: 'https://github.com/example/app',
              checked: 'Repository data checked October 2, 2026.'
            }
          }
        ]
      },
      attachTo: document.body
    });
    try {
      expect(wrapper.findAll('time')).toHaveLength(1);
      expect(wrapper.get('a').attributes('href')).toBe('https://github.com/example/app');
      expect(wrapper.text()).not.toContain('October 2');
      const trigger = wrapper.get('button');
      const tooltip = document.getElementById(trigger.attributes('aria-describedby')!);
      expect(tooltip?.getAttribute('role')).toBe('tooltip');
      expect(tooltip?.textContent).toContain('Repository data checked October 2, 2026.');
      trigger.element.focus();
      expect(document.activeElement).toBe(trigger.element);
      await trigger.trigger('click');
      await nextTick();
      await nextTick();
      expect(tooltip?.classList.contains('visible')).toBe(true);
      await trigger.trigger('keydown', { key: 'Escape' });
      expect(tooltip?.classList.contains('invisible')).toBe(true);
    } finally {
      wrapper.unmount();
    }
  });

  it('replaces disabled installation with the explanation and no copy control', () => {
    const wrapper = mount(OnPackageOverview, {
      props: {
        info: [],
        command: 'brew install --cask example',
        installNotice: { text: 'Homebrew disabled this app: discontinued.', disabled: true }
      },
      global: withLocale('en-US')
    });
    expect(wrapper.get('[data-testid="package-install-command"]').text()).toContain('discontinued');
    expect(wrapper.find('pre code').exists()).toBe(false);
    expect(wrapper.find('button').exists()).toBe(false);
    wrapper.unmount();
  });

  it('places the warning before a copyable command and keeps it available at mobile widths', () => {
    const wrapper = mount(OnPackageOverview, {
      props: {
        info: [],
        command: 'brew install --cask example',
        installNotice: { text: 'Recommendations paused; Homebrew still offers this app.', disabled: false }
      },
      global: withLocale('en-US')
    });
    const card = wrapper.get('[data-testid="package-install-command"]');
    expect(card.element.className).not.toContain('hidden');
    expect(
      card.get('[role="note"]').element.compareDocumentPosition(card.get('pre').element) &
        Node.DOCUMENT_POSITION_FOLLOWING
    ).toBeTruthy();
    expect(card.get('code').text()).toBe('brew install --cask example');
    expect(card.find('button').exists()).toBe(true);
    wrapper.unmount();
  });
});
