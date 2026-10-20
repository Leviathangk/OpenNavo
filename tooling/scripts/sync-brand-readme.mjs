// Keeps the definition block that opens each README's introduction identical to the website's one-sentence
// definition (landing.about.definition), so GitHub, the homepage and /llms.txt describe OpenNavo the same way.
//   node tooling/scripts/sync-brand-readme.mjs          rewrite the blocks from the website's message files
//   node tooling/scripts/sync-brand-readme.mjs --check  fail when a block is missing or out of date
import { readFileSync, writeFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';

const ROOT = new URL('../../', import.meta.url);

/** Each README and the website language its definition comes from. */
export const READMES = {
  'README.md': 'en-US',
  'docs/i18n/README.zh-CN.md': 'zh-CN',
  'docs/i18n/README.ja-JP.md': 'ja-JP',
  'docs/i18n/README.es-ES.md': 'es-ES',
  'docs/i18n/README.pt-BR.md': 'pt-BR',
  'docs/i18n/README.ru-RU.md': 'ru-RU'
};

const BLOCK = /<!-- brand-definition -->\n[\s\S]*?\n<!-- \/brand-definition -->/;

/** The README with its definition block replaced, or null when it has no block. */
export function withDefinition(readme, definition) {
  if (!BLOCK.test(readme)) return null;
  // A replacer function, so "$" in the definition is never read as a substitution pattern.
  return readme.replace(BLOCK, () => `<!-- brand-definition -->\n${definition}\n<!-- /brand-definition -->`);
}

function definitionFor(locale) {
  const messages = JSON.parse(readFileSync(new URL(`apps/web/i18n/locales/${locale}.json`, ROOT), 'utf8'));
  const text = messages.landing?.about?.definition;
  return typeof text === 'string' ? text.trim() : '';
}

if (process.argv[1] === fileURLToPath(import.meta.url)) {
  const check = process.argv.includes('--check');
  const problems = [];
  for (const [file, locale] of Object.entries(READMES)) {
    const url = new URL(file, ROOT);
    const readme = readFileSync(url, 'utf8');
    const definition = definitionFor(locale);
    const next = definition ? withDefinition(readme, definition) : readme;
    if (!definition) problems.push(`${file}: ${locale} has no landing.about.definition`);
    else if (next === null) problems.push(`${file}: no <!-- brand-definition --> block`);
    else if (next !== readme) {
      if (check) problems.push(`${file}: definition differs from ${locale} landing.about.definition`);
      else writeFileSync(url, next);
    }
  }
  if (problems.length) {
    console.error(problems.join('\n'));
    if (check) console.error('Run node tooling/scripts/sync-brand-readme.mjs to update the README definitions.');
    process.exit(1);
  }
}
