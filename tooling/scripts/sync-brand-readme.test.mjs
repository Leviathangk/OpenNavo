import assert from 'node:assert/strict';
import { existsSync, readFileSync } from 'node:fs';
import { test } from 'node:test';
import { READMES, withDefinition } from './sync-brand-readme.mjs';

test('replaces only the definition block and keeps $ literally', () => {
  const readme = '# OpenNavo\n\n<!-- brand-definition -->\nOld text.\n<!-- /brand-definition -->\n\nMore.\n';
  assert.equal(
    withDefinition(readme, 'Costs $0 and $& stays.'),
    '# OpenNavo\n\n<!-- brand-definition -->\nCosts $0 and $& stays.\n<!-- /brand-definition -->\n\nMore.\n'
  );
});

test('reports a README without a block', () => {
  assert.equal(withDefinition('# OpenNavo\n', 'Text.'), null);
});

test('maps every website language to an existing README', () => {
  const { locales } = JSON.parse(
    readFileSync(new URL('../../packages/shared/src/locales.json', import.meta.url), 'utf8')
  );
  assert.deepEqual(
    Object.values(READMES).sort(),
    locales
      .filter(locale => locale.surfaces.includes('site'))
      .map(locale => locale.code)
      .sort()
  );
  for (const file of Object.keys(READMES)) assert.ok(existsSync(new URL(`../../${file}`, import.meta.url)), file);
});
