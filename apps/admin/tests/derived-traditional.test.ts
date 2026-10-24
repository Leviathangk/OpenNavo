import { expect, it } from 'vitest';
import { CONTENT_LOCALES, SOURCE_LOCALE_OPTIONS, localizedBody } from '../src/utils/content-locale';

it('preserves Traditional previews but excludes it from authoring and writes', () => {
  expect(CONTENT_LOCALES).toContain('zh-Hant');
  expect(SOURCE_LOCALE_OPTIONS.map(option => option.value)).not.toContain('zh-Hant');
  expect(
    localizedBody({
      sourceLocale: 'zh-CN',
      keys: ['name'],
      texts: { 'zh-CN': { name: '软件' }, 'zh-Hant': { name: 'injected' } },
      original: {},
      entry: fields => fields
    })
  ).toEqual({ 'zh-CN': { name: '软件' } });
});
