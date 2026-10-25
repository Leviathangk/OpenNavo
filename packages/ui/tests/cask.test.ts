import { mount } from '@vue/test-utils';
import { describe, expect, it } from 'vitest';
import { createSSRApp, ref } from 'vue';
import { renderToString } from 'vue/server-renderer';
import type { CaskArtifact } from '@opennavo/api';
import { LOCALES, sharedMessages } from '@opennavo/shared';
import OnCaskArtifacts from '../src/components/OnCaskArtifacts.vue';
import { ON_UI_LOCALE } from '../src/composables/locale';
import { withLocale } from './helpers';

describe('Cask installation declarations', () => {
  for (const locale of LOCALES) {
    it(`${locale}: renders fonts, paths, uninstall cleanup, and escaped scripts`, () => {
      const script = '<script>window.bad = true</script>';
      const wrapper = mount(OnCaskArtifacts, {
        global: withLocale(locale),
        props: {
          entries: [
            {
              type: 'font',
              phase: 'install',
              sources: ['Font.otf'],
              target: null,
              declaration: { font: ['Font.otf'] }
            },
            {
              type: 'app',
              phase: 'install',
              sources: ['Original.app'],
              target: 'Renamed.app',
              declaration: { app: ['Original.app', { target: 'Renamed.app' }] }
            },
            { type: 'uninstall', phase: 'uninstall', sources: [], declaration: { uninstall: [{ script }] } },
            { type: 'zap', phase: 'cleanup', sources: [], declaration: { zap: [{ trash: '~/.test' }] } }
          ]
        }
      });
      expect(wrapper.text()).toContain(sharedMessages[locale].cask.declarations);
      expect(wrapper.text()).toContain(sharedMessages[locale].cask.cleanup);
      expect(wrapper.text()).toContain('Font.otf');
      expect(wrapper.text()).toContain('Renamed.app');
      expect(wrapper.text()).toContain('~/.test');
      expect(wrapper.text()).not.toContain('/Applications/');
      expect(wrapper.find('script').exists()).toBe(false);
      expect(wrapper.findAll('details')).toHaveLength(4);
    });
  }
});

describe('Long declaration lists', () => {
  // Like font-monaspace-frozen: 210 font files, then the uninstall declaration.
  const fonts = (count: number): CaskArtifact[] =>
    Array.from({ length: count }, (_, index) => ({
      type: 'font',
      phase: 'install' as const,
      sources: [`Frozen Fonts/Monaspace Argon/MonaspaceArgonFrozen-${index}.ttf`],
      target: null,
      declaration: { font: [`Frozen Fonts/Monaspace Argon/MonaspaceArgonFrozen-${index}.ttf`] }
    }));
  const entries = [...fonts(210), { type: 'uninstall', phase: 'uninstall' as const, sources: [], declaration: {} }];

  it('Server-render the first twenty entries and the full count', async () => {
    const app = createSSRApp(OnCaskArtifacts, { entries });
    app.provide(ON_UI_LOCALE, ref('en-US'));
    const html = await renderToString(app);
    expect(html.match(/<li/g)).toHaveLength(20);
    // Code text breaks file names after the dot.
    expect(html).toContain('MonaspaceArgonFrozen-19.');
    expect(html).not.toContain('MonaspaceArgonFrozen-20.');
    expect(html).toContain('View all 211');
  });

  it('Show the rest on request and fold it again', async () => {
    const wrapper = mount(OnCaskArtifacts, { global: withLocale('zh-CN'), props: { entries } });
    const button = wrapper.get('button');
    expect(wrapper.findAll('li')).toHaveLength(20);
    expect(button.text()).toBe('查看全部 211 款');
    expect(button.attributes('aria-expanded')).toBe('false');
    expect(button.attributes('aria-controls')).toBe(wrapper.get('ul').attributes('id'));
    await button.trigger('click');
    expect(wrapper.findAll('li')).toHaveLength(211);
    expect(button.text()).toBe(sharedMessages['zh-CN'].about.collapse);
    expect(button.attributes('aria-expanded')).toBe('true');
    await button.trigger('click');
    expect(wrapper.findAll('li')).toHaveLength(20);
  });

  it('Keep lists of up to thirty entries whole', () => {
    const wrapper = mount(OnCaskArtifacts, { global: withLocale('en-US'), props: { entries: fonts(30) } });
    expect(wrapper.findAll('li')).toHaveLength(30);
    expect(wrapper.find('button').exists()).toBe(false);
  });
});
