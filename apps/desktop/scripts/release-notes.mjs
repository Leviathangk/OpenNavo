import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { pathToFileURL } from 'node:url';
import { parseArgs } from 'node:util';

// Client release notes follow the app locales; site-only languages (de, fr, it, zh-Hant) are not shipped in the app.
export const RELEASE_NOTE_LOCALES = JSON.parse(
  readFileSync(new URL('../../../packages/shared/src/locales.json', import.meta.url), 'utf8')
)
  .locales.filter(locale => locale.surfaces.includes('app'))
  .map(locale => locale.code);

export function validateNotes(notes, version, locale) {
  const text = typeof notes === 'string' ? notes.trim() : '';
  const plain = text.replace(/[#*`_]/g, '').trim();
  if (!text || plain === `OpenNavo ${version}` || plain === version || /^(TODO|TBD|待补充|待填写)$/i.test(plain)) {
    throw new Error(`Missing real release notes for ${locale}; provide a Markdown file with user-facing changes`);
  }
  if (locale === 'en-US' && /\p{Script=Han}/u.test(text)) {
    throw new Error('English release notes must not contain Chinese text');
  }
  return text;
}

export function loadReleaseNotes(dir, version) {
  if (!/^\d+\.\d+\.\d+(-[0-9A-Za-z.]+)?$/.test(version)) throw new Error('Invalid release notes version');
  return Object.fromEntries(
    RELEASE_NOTE_LOCALES.map(locale => [
      locale,
      validateNotes(readFileSync(resolve(dir, version, `${locale}.md`), 'utf8'), version, locale)
    ])
  );
}

if (process.argv[1] && import.meta.url === pathToFileURL(resolve(process.argv[1])).href) {
  const { values } = parseArgs({
    options: {
      dir: { type: 'string', default: 'apps/desktop/release-notes' },
      version: { type: 'string' }
    }
  });
  loadReleaseNotes(values.dir, values.version ?? '');
  console.log(`Release notes validated for ${values.version} (${RELEASE_NOTE_LOCALES.join(', ')})`);
}
