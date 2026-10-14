import { mount } from '@vue/test-utils';
import { describe, expect, it } from 'vitest';
import { LOCALES, sharedMessages } from '@opennavo/shared';
import OnCaskArtifacts from '../src/components/OnCaskArtifacts.vue';
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
