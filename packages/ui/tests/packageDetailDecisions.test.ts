import { mount } from '@vue/test-utils';
import { createSSRApp, h, nextTick } from 'vue';
import { renderToString } from 'vue/server-renderer';
import { describe, expect, it } from 'vitest';
import OnPackageSpecCard from '../src/components/OnPackageSpecCard.vue';
import OnPackageOverview from '../src/components/OnPackageOverview.vue';
import { withLocale } from './helpers';

describe('Package detail product decisions', () => {
  it('includes the repository aria-describedby association in SSR output', async () => {
    const context: { teleports?: Record<string, string> } = {};
    const html = await renderToString(
      createSSRApp(OnPackageSpecCard, {
        checkedLabel: 'Checked',
        rows: [
          {
            key: 'repository',
            label: 'Repository',
            stars: {
              label: '★12.3K',
              checked: 'Repository data checked October 2, 2026.'
            }
          }
        ]
      }),
      context
    );
    const page = document.createElement('div');
    page.innerHTML = html + (context.teleports?.body ?? '');
    const id = page.querySelector('button')?.getAttribute('aria-describedby');
    expect(id).toBeTruthy();
    const description = page.querySelector(`[id="${id}"]`);
    expect(description?.getAttribute('role')).toBe('tooltip');
    expect(description?.textContent).toContain('Repository data checked October 2, 2026.');
  });

  it('uses standard external-link attributes and styling for https URLs', () => {
    const wrapper = mount(OnPackageSpecCard, {
      props: {
        rows: [
          {
            key: 'repository',
            label: 'Repository',
            value: 'GitHub repository',
            href: 'https://github.com/acme/app',
            stars: { label: '★12.3K', href: 'https://github.com/acme/app' }
          },
          { key: 'homepage', label: 'Homepage', value: 'example.com', href: 'https://example.com/' }
        ]
      }
    });
    try {
      const links = wrapper.findAll('a');
      expect(links).toHaveLength(3);
      for (const link of links) {
        expect(link.attributes('href')?.startsWith('https://')).toBe(true);
        expect(link.attributes('target')).toBe('_blank');
        expect(link.attributes('rel')).toBe('noopener noreferrer');
        expect(link.classes()).toEqual(
          expect.arrayContaining(['inline-flex', 'gap-4px', 'no-underline', 'hover:underline'])
        );
        expect(link.find('svg').exists()).toBe(true);
      }
    } finally {
      wrapper.unmount();
    }
  });

  it('renders no link unless href and stars.href use the https scheme', () => {
    const wrapper = mount(OnPackageSpecCard, {
      props: {
        rows: [
          {
            key: 'repository',
            label: 'Repository',
            value: 'Open source',
            href: 'javascript:alert(1)',
            stars: { label: '★12.3K', href: 'javascript:alert(1)' }
          },
          { key: 'homepage', label: 'Homepage', value: 'Insecure example', href: 'http://example.com/' },
          { key: 'internal', label: 'Related', value: 'Related app', href: '/apps/example' }
        ]
      }
    });
    try {
      expect(wrapper.findAll('a')).toHaveLength(0);
      expect(wrapper.html()).not.toContain('javascript:');
      expect(wrapper.text()).toContain('Open source · ★12.3K');
      expect(wrapper.text()).toContain('Insecure example');
      expect(wrapper.text()).toContain('Related app');
    } finally {
      wrapper.unmount();
    }
  });

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
