import { describe, expect, it } from 'vitest';
import { createI18n } from 'vue-i18n';
import { DESKTOP_LOCALES, LOCALE_METADATA, pluralRules } from '@opennavo/shared';
import enUS from '../src/i18n/en-US';
import zhCN from '../src/i18n/zh-CN';

describe('vue-i18n plural integration', () => {
  it.each(DESKTOP_LOCALES)('%s preserves formatted interpolation and selects categories using raw counts', locale => {
    const i18n = createI18n({
      legacy: false,
      locale,
      pluralRules,
      messages: {
        [locale]: {
          forms: LOCALE_METADATA[locale].pluralCategories.map(category => `${category} {count}`).join(' | ')
        }
      }
    });
    for (const count of [0, 1, 2, 5, 21]) {
      const selected = new Intl.PluralRules(locale).select(count);
      expect(i18n.global.t('forms', { count: 'formatted' }, { plural: count })).toBe(`${selected} formatted`);
    }
  });

  it('English update/task titles use singular forms; Chinese output remains unchanged', () => {
    const i18n = createI18n({
      legacy: false,
      locale: 'en-US',
      pluralRules,
      messages: { 'en-US': enUS, 'zh-CN': zhCN }
    });
    expect(i18n.global.t('nav.updatesBadge', { count: 1 }, { plural: 1 })).toBe('1 update available');
    expect(i18n.global.t('quit.title', { n: 1 }, { plural: 1 })).toBe('1 task is not finished');
    expect(i18n.global.t('nav.updatesBadge', { count: 21 }, { plural: 21 })).toBe('21 updates available');
    i18n.global.locale.value = 'zh-CN';
    expect(i18n.global.t('nav.updatesBadge', { count: 1 }, { plural: 1 })).toBe(
      zhCN.nav.updatesBadge.replace('{count}', '1')
    );
  });
});
