import { describe, expect, it, vi } from 'vitest';
import dayjs from 'dayjs';
import { LOCALES } from '@opennavo/shared';
import messages from '../src/locales/locale';
import { resolveLocale } from '../src/locales/preference';
import { naiveDateLocales, naiveLocales } from '../src/locales/naive';
import { setDayjsLocale } from '../src/locales/dayjs';
vi.mock('@/locales', () => ({ getLocale: () => 'en-US' }));
vi.mock('../src/locales/index', () => ({ getLocale: () => 'en-US' }));

describe('Admin six-language runtime configuration', () => {
  it.each(['ja-JP', 'es-ES', 'pt-BR', 'ru-RU'] as const)(
    '%s uses complete generated messages without English placeholders',
    locale => {
      expect(messages[locale]).not.toBe(messages['en-US']);
      // Brand titles may remain English; generic actions must use generated translations.
      expect(messages[locale].common.cancel).not.toBe(messages['en-US'].common.cancel);
      expect(Object.keys(messages[locale].page)).toEqual(Object.keys(messages['zh-CN'].page));
    }
  );
  it('Stored choices win; invalid values match browser preferences; unknown falls back to English', () => {
    expect(resolveLocale('ru-RU', ['ja-JP'])).toBe('ru-RU');
    expect(resolveLocale('invalid', ['fr-FR', 'es-MX'])).toBe('es-ES');
    expect(resolveLocale(null, ['pt-PT'])).toBe('pt-BR');
    expect(resolveLocale(undefined, ['fr-FR'])).toBe('en-US');
  });
  it.each(LOCALES)('%s supplies component, date, and dayjs locale data', code => {
    expect(naiveLocales[code].name).toBeTruthy();
    expect(naiveDateLocales[code].name).toBeTruthy();
    setDayjsLocale(code);
    expect(dayjs.locale()).toBe(
      { 'en-US': 'en', 'zh-CN': 'zh-cn', 'ja-JP': 'ja', 'es-ES': 'es', 'pt-BR': 'pt-br', 'ru-RU': 'ru' }[code]
    );
  });
  it('dayjs defaults follow UI locale', () => {
    setDayjsLocale();
    expect(dayjs.locale()).toBe('en');
  });
});
