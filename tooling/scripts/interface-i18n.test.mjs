import { test } from 'node:test';
import assert from 'node:assert/strict';
import { validate, loadTarget, flatten, inflate, translatedCodes } from './interface-i18n.mjs';
// The Mac client's native strings are translated into every desktop language except the zh-CN and en-US sources.
const NATIVE = translatedCodes('native');
test('Admin export includes the expanded page object', () => {
  const target = loadTarget('admin');
  assert.ok(flatten(target.source)['page.shared.i18n.sourceLocale']);
  validate(flatten(target.source), flatten(target.english), flatten(target.english), 'en-US', 'admin');
});
test('Invalid keys, placeholders, and plural forms are rejected', () => {
  const source = { count: '{count} 个更新' },
    english = { count: '{count} update | {count} updates' };
  assert.throws(() => validate(source, english, {}, 'ru-RU', 'test'), /keys/);
  assert.throws(
    () => validate(source, english, { count: '{n} update | {n} updates' }, 'en-US', 'test'),
    /Placeholders/
  );
  assert.throws(() => validate(source, english, { count: '{count} обновление' }, 'ru-RU', 'test'), /Plural/);
  validate(
    source,
    english,
    { count: '{count} обновление | {count} обновления | {count} обновлений | {count} обновления' },
    'ru-RU',
    'test'
  );
});

test('Failed writes persist nothing; successful writes update selected targets and preserve manual translations', async () => {
  const fs = await import('node:fs');
  const { exportPlan, writeResult, hash } = await import('./interface-i18n.mjs');
  const root = process.cwd();
  fs.mkdirSync('apps/server/tmp/i18n-sync', { recursive: true });
  const directory = fs.mkdtempSync(`${root}/apps/server/tmp/i18n-sync/test-`);
  try {
    process.chdir(directory);
    fs.mkdirSync('tooling/i18n', { recursive: true });
    const folder = 'apps/desktop/src-tauri/resources/locales';
    fs.mkdirSync(folder, { recursive: true });
    for (const code of ['zh-CN', 'en-US', ...NATIVE])
      fs.writeFileSync(`${folder}/${code}.json`, JSON.stringify({ key: code === 'zh-CN' ? '打开' : `manual-${code}` }));
    const lock = {
      version: 2,
      targets: { native: { sources: { key: hash('打开') } }, web: { sources: { keep: 'untouched' } } }
    };
    fs.writeFileSync('tooling/i18n/i18n.lock.json', JSON.stringify(lock));
    const plan = exportPlan(['native']),
      result = { native: structuredClone(plan.targets.native.translations) };
    const before = fs.readFileSync('tooling/i18n/i18n.lock.json', 'utf8');
    assert.throws(() => writeResult(plan, { native: { ...result.native, 'ru-RU': { wrong: 'bad' } } }));
    assert.equal(fs.readFileSync('tooling/i18n/i18n.lock.json', 'utf8'), before);
    assert.equal(JSON.parse(fs.readFileSync(`${folder}/ru-RU.json`)).key, 'manual-ru-RU');
    writeResult(plan, result);
    assert.deepEqual(JSON.parse(fs.readFileSync('tooling/i18n/i18n.lock.json')).targets.web, lock.targets.web);
    assert.equal(JSON.parse(fs.readFileSync(`${folder}/ru-RU.json`)).key, 'manual-ru-RU');
    fs.writeFileSync(`${folder}/zh-CN.json`, JSON.stringify({ key: '改变' }));
    assert.throws(() => writeResult(plan, result), /during/);
  } finally {
    process.chdir(root);
    fs.rmSync(directory, { recursive: true, force: true });
  }
});

test('Exporting and writing array text preserves array structure', () => {
  const source = { body: ['第一段', '第二段'], nested: { key: '源文' } };
  assert.deepEqual(inflate({ 'body.0': 'One', 'body.1': 'Two', 'nested.key': 'Text' }, source), {
    body: ['One', 'Two'],
    nested: { key: 'Text' }
  });
});

test('Offline validation rejects invalid keys, placeholders, and plurals', async () => {
  const fs = await import('node:fs'),
    { spawnSync } = await import('node:child_process');
  const root = process.cwd(),
    script = `${root}/tooling/scripts/check-interface-i18n.mjs`;
  fs.mkdirSync('apps/server/tmp/i18n-sync', { recursive: true });
  const directory = fs.mkdtempSync(`${root}/apps/server/tmp/i18n-sync/gate-`);
  try {
    const folder = `${directory}/apps/desktop/src-tauri/resources/locales`;
    fs.mkdirSync(folder, { recursive: true });
    fs.mkdirSync(`${directory}/packages/shared/src`, { recursive: true });
    fs.copyFileSync(`${root}/packages/shared/src/locales.json`, `${directory}/packages/shared/src/locales.json`);
    const texts = {
      'zh-CN': '{count} 个更新',
      'en-US': '{count} update | {count} updates',
      'ja-JP': '{count} 更新',
      'es-ES': '{count} actualización | {count} actualizaciones',
      'pt-BR': '{count} atualização | {count} atualizações',
      'ru-RU': '{count} обновление | {count} обновления | {count} обновлений | {count} обновления',
      'de-DE': '{count} Update | {count} Updates',
      'fr-FR': '{count} mise à jour | {count} mises à jour',
      'it-IT': '{count} aggiornamento | {count} aggiornamenti',
      'zh-Hant': '{count} 個更新'
    };
    for (const [code, value] of Object.entries(texts))
      fs.writeFileSync(`${folder}/${code}.json`, JSON.stringify({ key: value }));
    const { hash } = await import('./interface-i18n.mjs');
    fs.mkdirSync(`${directory}/tooling/i18n`, { recursive: true });
    fs.writeFileSync(
      `${directory}/tooling/i18n/i18n.lock.json`,
      JSON.stringify({ version: 2, targets: { native: { sources: { key: hash(texts['zh-CN']) } } } })
    );
    const run = () =>
      spawnSync(process.execPath, [script, '--targets', 'native'], { cwd: directory, encoding: 'utf8' });
    assert.equal(run().status, 0);
    for (const [value, message] of [
      [{ wrong: texts['ru-RU'] }, 'Locale keys'],
      [{ key: texts['ru-RU'].replaceAll('{count}', '{bad}') }, 'Placeholders'],
      [{ key: '{count} обновление' }, 'Plural']
    ]) {
      fs.writeFileSync(`${folder}/ru-RU.json`, JSON.stringify(value));
      const result = run();
      assert.notEqual(result.status, 0);
      assert.ok(result.stderr.includes(message));
    }
    fs.unlinkSync(`${folder}/ru-RU.json`);
    const missing = run();
    assert.notEqual(missing.status, 0);
    assert.ok(missing.stderr.includes('Missing locale file: apps/desktop/src-tauri/resources/locales/ru-RU.json'));
    assert.ok(missing.stderr.includes('make i18n-sync ARGS="--targets native"'));
    assert.ok(!missing.stderr.includes('ENOENT') && !missing.stderr.includes(' at '));
  } finally {
    fs.rmSync(directory, { recursive: true, force: true });
  }
});

test('Partial success persists; lock completes only keys translated into every language, and source changes allow per-language resumption', async () => {
  const fs = await import('node:fs');
  const { exportPlan, writeResult, hash } = await import('./interface-i18n.mjs');
  const root = process.cwd();
  const directory = fs.mkdtempSync(`${root}/apps/server/tmp/i18n-sync/partial-`);
  try {
    process.chdir(directory);
    fs.mkdirSync('tooling/i18n', { recursive: true });
    const folder = 'apps/desktop/src-tauri/resources/locales';
    fs.mkdirSync(folder, { recursive: true });
    fs.writeFileSync(`${folder}/zh-CN.json`, JSON.stringify({ key: '新的源文', keep: '保留' }));
    fs.writeFileSync(`${folder}/en-US.json`, JSON.stringify({ key: 'New source', keep: 'Keep' }));
    for (const code of NATIVE)
      fs.writeFileSync(`${folder}/${code}.json`, JSON.stringify({ key: 'Old', keep: `manual-${code}` }));
    fs.writeFileSync(
      'tooling/i18n/i18n.lock.json',
      JSON.stringify({
        version: 2,
        targets: {
          native: { sources: { key: hash('旧的源文'), keep: hash('保留') } },
          web: { sources: { untouched: 'yes' } }
        }
      })
    );
    const plan = exportPlan(['native']);
    const result = {
      native: Object.fromEntries(
        Object.entries(plan.targets.native.translations).map(([code, values]) => [
          code,
          { keep: values.keep, ...(code === 'ja-JP' ? { key: '新しい文章' } : {}) }
        ])
      )
    };
    writeResult(plan, result, true);
    assert.equal(JSON.parse(fs.readFileSync(`${folder}/ja-JP.json`)).key, '新しい文章');
    assert.equal(JSON.parse(fs.readFileSync(`${folder}/es-ES.json`)).key, undefined);
    assert.equal(
      JSON.parse(fs.readFileSync('tooling/i18n/i18n.lock.json')).targets.native.sources.key,
      hash('旧的源文')
    );
    assert.deepEqual(JSON.parse(fs.readFileSync('tooling/i18n/i18n.lock.json')).targets.web, {
      sources: { untouched: 'yes' }
    });
    const next = exportPlan(['native']);
    assert.equal(next.targets.native.localeHashes['ja-JP'].key, hash('新的源文'));
    assert.equal(next.targets.native.translations['es-ES'].key, undefined);
    // Do not reuse stale completion records after a manual translation edit.
    fs.writeFileSync(`${folder}/ja-JP.json`, JSON.stringify({ key: 'Manual changed', keep: 'manual-ja-JP' }));
    assert.equal(exportPlan(['native']).targets.native.localeHashes?.['ja-JP']?.key, undefined);
  } finally {
    process.chdir(root);
    fs.rmSync(directory, { recursive: true, force: true });
  }
});

test('Partial arrays do not create null or shift indices; completed values restore arrays', () => {
  const template = { body: ['第一段', '第二段'] };
  const partial = JSON.parse(JSON.stringify(inflate({ 'body.1': 'Second' }, template)));
  assert.deepEqual(flatten(partial), { 'body.1': 'Second' });
  assert.deepEqual(inflate({ 'body.0': 'First', 'body.1': 'Second' }, template), { body: ['First', 'Second'] });
});

test('Native separators follow fixed locale rules and leave other target locks unchanged', async () => {
  const fs = await import('node:fs'),
    path = await import('node:path');
  const { exportPlan, writeResult } = await import('./interface-i18n.mjs');
  const root = process.cwd(),
    directory = fs.mkdtempSync(path.join(root, 'apps/server/tmp/i18n-punctuation-test-'));
  try {
    process.chdir(directory);
    fs.mkdirSync('tooling/i18n', { recursive: true });
    const folder = 'apps/desktop/src-tauri/resources/locales';
    fs.mkdirSync(folder, { recursive: true });
    fs.writeFileSync(`${folder}/zh-CN.json`, JSON.stringify({ separator: '、' }));
    fs.writeFileSync(`${folder}/en-US.json`, JSON.stringify({ separator: ', ' }));
    for (const code of NATIVE) fs.writeFileSync(`${folder}/${code}.json`, JSON.stringify({ separator: '、' }));
    fs.writeFileSync(
      'tooling/i18n/i18n.lock.json',
      JSON.stringify({ version: 2, targets: { web: { sources: { untouched: 'yes' } } } })
    );
    const plan = exportPlan(['native']);
    const result = {
      native: Object.fromEntries(NATIVE.map(code => [code, { separator: '、' }]))
    };
    writeResult(plan, result, true);
    for (const code of NATIVE)
      assert.equal(
        JSON.parse(fs.readFileSync(`${folder}/${code}.json`)).separator,
        ['ja-JP', 'zh-Hant'].includes(code) ? '、' : ', '
      );
    assert.deepEqual(JSON.parse(fs.readFileSync('tooling/i18n/i18n.lock.json')).targets.web, {
      sources: { untouched: 'yes' }
    });
  } finally {
    process.chdir(root);
    fs.rmSync(directory, { recursive: true, force: true });
  }
});
