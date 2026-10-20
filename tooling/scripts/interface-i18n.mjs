// Share parsing between synchronization and offline checks to prevent key-space drift.
import { readFileSync, writeFileSync, existsSync, mkdirSync, rmdirSync, renameSync } from 'node:fs';
import { createHash } from 'node:crypto';
import vm from 'node:vm';
import ts from 'typescript';
export const read = path => JSON.parse(readFileSync(path, 'utf8'));
export const manifest = read('packages/shared/src/locales.json');
// web and shared strings ship on the website, so they cover every site locale; the other targets are app surfaces.
const SITE_TARGETS = new Set(['web', 'shared']);
export const targetLocales = name =>
  SITE_TARGETS.has(name) ? manifest.locales : manifest.locales.filter(locale => locale.surfaces.includes('app'));
/** Locales a target needs translated files for: everything except the zh-CN source and the en-US authoring text. */
export const translatedCodes = name =>
  targetLocales(name)
    .map(locale => locale.code)
    .filter(code => !['zh-CN', 'en-US'].includes(code));
export const hash = value => createHash('sha256').update(value).digest('hex');
export function sourceObject(path, name, bindings = {}) {
  const ast = ts.createSourceFile(path, readFileSync(path, 'utf8'), ts.ScriptTarget.Latest, true);
  let initializer;
  for (const statement of ast.statements) {
    if (!name && ts.isExportAssignment(statement)) {
      if (ts.isIdentifier(statement.expression)) return sourceObject(path, statement.expression.text, bindings);
      initializer = statement.expression;
    }
    if (name && ts.isVariableStatement(statement))
      for (const declaration of statement.declarationList.declarations) {
        if (declaration.name.getText(ast) === name) initializer = declaration.initializer;
      }
  }
  if (!initializer) throw new Error(`Missing locale source object: ${path}/${name ?? 'default'}`);
  const context = { ...bindings };
  vm.runInNewContext(ts.transpileModule(`globalThis.data = ${initializer.getText(ast)}`, {}).outputText, context, {
    timeout: 1000
  });
  return context.data;
}
export function flatten(value, prefix = '') {
  if (typeof value === 'string') return { [prefix]: value };
  if (!value || typeof value !== 'object') throw new Error(`Invalid text: ${prefix}`);
  return Object.assign(
    {},
    ...Object.entries(value).map(([key, child]) => flatten(child, prefix ? `${prefix}.${key}` : key))
  );
}
export function inflate(flat, template, prefix = '') {
  if (typeof template === 'string') return flat[prefix];
  const entries = Object.entries(template).map(([key, value]) => [
    key,
    inflate(flat, value, prefix ? `${prefix}.${key}` : key)
  ]);
  // Store incomplete arrays with numeric keys to avoid JSON nulls or shifted indices; restore arrays when complete.
  return Array.isArray(template) && entries.every(([, value]) => value !== undefined)
    ? entries.map(([, value]) => value)
    : Object.fromEntries(entries);
}
export const targetNames = ['web', 'desktop', 'shared', 'native', 'admin', 'server'];
export function loadTarget(name) {
  const jsonTarget = directory => ({
    source: read(`${directory}/zh-CN.json`),
    english: read(`${directory}/en-US.json`),
    path: code => `${directory}/${code}.json`
  });
  switch (name) {
    case 'web':
      return jsonTarget('apps/web/i18n/locales');
    case 'native':
      return jsonTarget('apps/desktop/src-tauri/resources/locales');
    case 'server':
      return jsonTarget('apps/server/internal/i18n/messages');
    case 'desktop':
      return {
        source: sourceObject('apps/desktop/src/i18n/zh-CN.ts'),
        english: sourceObject('apps/desktop/src/i18n/en-US.ts', 'messages'),
        path: code => `apps/desktop/src/i18n/${code}.json`
      };
    case 'shared':
      return {
        source: sourceObject('packages/shared/src/messages.ts', 'zhCN'),
        english: sourceObject('packages/shared/src/messages.ts', 'enUS'),
        path: code => `packages/shared/src/messages/${code}.json`
      };
    case 'admin':
      return {
        source: sourceObject('apps/admin/src/locales/langs/zh-cn.ts', 'local', {
          pages: sourceObject('apps/admin/src/locales/langs/pages/zh-cn.ts')
        }),
        english: sourceObject('apps/admin/src/locales/langs/en-us.ts', 'local', {
          pages: sourceObject('apps/admin/src/locales/langs/pages/en-us.ts')
        }),
        path: code => `apps/admin/src/locales/langs/${code.toLowerCase()}.json`
      };
    default:
      throw new Error(`Unknown target: ${name}`);
  }
}
export function lockFile() {
  const lock = read('tooling/i18n/i18n.lock.json');
  if (lock.version === 1)
    return {
      version: 2,
      targets: Object.fromEntries(Object.entries(lock.sources).map(([name, sources]) => [name, { sources }]))
    };
  if (lock.version !== 2 || !lock.targets) throw new Error('Invalid lock-file format');
  return lock;
}
const placeholders = text =>
  [...text.matchAll(/\{[A-Za-z_][A-Za-z0-9_]*\}/g)]
    .map(x => x[0])
    .sort()
    .join(',');
export function validate(source, english, translated, code, label) {
  if (Object.keys(source).sort().join('\n') !== Object.keys(translated).sort().join('\n'))
    throw new Error(`Locale keys do not match: ${label}/${code}`);
  for (const [key, value] of Object.entries(source)) {
    const plural = value.includes('|') || english[key]?.includes('|');
    const forms = translated[key].split('|').map(x => x.trim());
    const expected = plural ? manifest.locales.find(x => x.code === code).pluralCategories.length : 1;
    if (forms.length !== expected) throw new Error(`Plural forms do not match: ${label}/${code}/${key}`);
    const src = value.split('|').map(x => x.trim());
    for (const form of forms)
      if (!form || placeholders(form) !== placeholders(src[0]))
        throw new Error(`Placeholders do not match: ${label}/${code}/${key}`);
  }
}
export function selection(args) {
  const index = args.indexOf('--targets');
  const value =
    args.find(x => x.startsWith('--targets='))?.slice(10) ?? (index >= 0 ? args[index + 1] : targetNames.join(','));
  const names = [...new Set(value.split(','))];
  if (names.some(x => !targetNames.includes(x))) throw new Error('Unknown targets');
  return names;
}
const progressPath = 'apps/server/tmp/i18n-sync/completed.json';
export function exportPlan(names) {
  const lock = lockFile();
  const targets = {};
  const progress = existsSync(progressPath) ? read(progressPath) : {};
  for (const name of names) {
    const target = loadTarget(name),
      source = flatten(target.source),
      english = flatten(target.english);
    validate(source, english, source, 'zh-CN', name);
    validate(source, english, english, 'en-US', name);
    const translations = Object.fromEntries(
      translatedCodes(name).map(code => [code, existsSync(target.path(code)) ? flatten(read(target.path(code))) : {}])
    );
    const localeHashes = {};
    for (const [code, values] of Object.entries(translations)) {
      for (const [key, entry] of Object.entries(progress[name]?.[code] ?? {})) {
        if (entry.translation === hash(values[key] ?? '') && entry.source === hash(source[key] ?? '')) {
          (localeHashes[code] ??= {})[key] = entry.source;
        }
      }
    }
    targets[name] = {
      source,
      english,
      translations,
      hashes: lock.targets[name]?.sources ?? {},
      ...(Object.keys(localeHashes).length ? { localeHashes } : {})
    };
  }
  return { locales: manifest.locales, targets };
}
export function withLock(fn) {
  const directory = 'tooling/i18n/i18n.lock.json.write-lock';
  const deadline = Date.now() + 30000;
  while (true) {
    try {
      mkdirSync(directory);
      break;
    } catch (error) {
      if (error.code !== 'EEXIST' || Date.now() >= deadline)
        throw new Error('Lock file is being written; ensure other synchronization processes have finished');
      Atomics.wait(new Int32Array(new SharedArrayBuffer(4)), 0, 0, 100);
    }
  }
  try {
    return fn();
  } finally {
    rmdirSync(directory);
  }
}
export function writeResult(plan, result, partial = false) {
  // Separators follow fixed locale punctuation rules; do not let the model translate commas into enumeration commas.
  if (plan.targets.native)
    for (const [code, values] of Object.entries(result.native ?? {})) {
      if ('separator' in values) values.separator = ['zh-CN', 'ja-JP'].includes(code) ? '、' : ', ';
    }
  // Go returns only keys that pass batch validation; still reject extra keys, invalid placeholders, and invalid plurals before writing.
  for (const [name, target] of Object.entries(plan.targets))
    for (const [code, values] of Object.entries(result[name] ?? {})) {
      if (Object.keys(values).some(key => !(key in target.source)))
        throw new Error(`Extra translation keys: ${name}/${code}`);
      const source = partial
        ? Object.fromEntries(Object.keys(values).map(key => [key, target.source[key]]))
        : target.source;
      validate(source, target.english, values, code, name);
    }
  for (const name of Object.keys(plan.targets))
    if (
      Object.keys(result[name] ?? {})
        .sort()
        .join(',') !== translatedCodes(name).sort().join(',')
    )
      throw new Error(`Missing translation results: ${name}`);
  withLock(() => {
    const current = exportPlan(Object.keys(plan.targets));
    if (JSON.stringify(current) !== JSON.stringify(plan))
      throw new Error('Source text, translations, or target lock changed during synchronization; rerun');
    const lock = lockFile();
    const progress = existsSync(progressPath) ? read(progressPath) : {};
    for (const [name, target] of Object.entries(plan.targets)) {
      const definition = loadTarget(name);
      for (const [code, values] of Object.entries(result[name])) {
        const path = definition.path(code),
          temporary = `${path}.i18n-${process.pid}`;
        writeFileSync(temporary, JSON.stringify(inflate(values, definition.source), null, 2) + '\n');
        renameSync(temporary, path);
      }
      const oldSources = lock.targets[name]?.sources ?? {};
      const sources = Object.fromEntries(
        Object.entries(target.source).flatMap(([key, value]) => {
          if (Object.values(result[name]).every(fields => key in fields)) return [[key, hash(value)]];
          return key in oldSources ? [[key, oldSources[key]]] : [];
        })
      );
      lock.targets[name] = { ...(lock.targets[name] ?? {}), sources };
      progress[name] = Object.fromEntries(
        Object.entries(result[name]).map(([code, values]) => [
          code,
          Object.fromEntries(
            Object.entries(values).map(([key, value]) => [
              key,
              { source: hash(target.source[key]), translation: hash(value) }
            ])
          )
        ])
      );
    }
    mkdirSync('apps/server/tmp/i18n-sync', { recursive: true });
    const progressTemporary = `${progressPath}.${process.pid}`;
    writeFileSync(progressTemporary, JSON.stringify(progress) + '\n', { mode: 0o600 });
    renameSync(progressTemporary, progressPath);
    const temporary = `tooling/i18n/i18n.lock.json.${process.pid}`;
    writeFileSync(temporary, JSON.stringify(lock, null, 2) + '\n');
    renameSync(temporary, 'tooling/i18n/i18n.lock.json');
  });
}
