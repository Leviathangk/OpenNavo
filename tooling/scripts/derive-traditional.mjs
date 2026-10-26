import { convertTraditional } from './traditional-converter.mjs';
import { readFileSync, writeFileSync, readdirSync, existsSync } from 'node:fs';
import { resolve } from 'node:path';
import { pathToFileURL } from 'node:url';
import { loadTarget } from './interface-i18n.mjs';

const TERMS = 'tooling/i18n/zh-Hant-terms.json';
const OVERRIDES = 'tooling/i18n/zh-Hant-overrides.json';
const CONVERTER_TERMS = 'apps/server/internal/traditional/data/opennavo-terms.json';

/**
 * Applies the repository's context wording on top of the converter, which already applied the unconditional Taiwan
 * wording: replacements scoped by key and decided by the English source, then pinned single strings. A pin whose
 * Simplified source changed, or that matches no string, fails, so a stale pin cannot hide a new source text.
 */
export function refineTraditional(path, source, english, converted, rules, pins = []) {
  if (rules.replace) throw new Error(`${TERMS} has replace rules; unconditional wording lives in ${CONVERTER_TERMS}`);
  const used = new Set();
  const walk = (from, en, value, keys) => {
    if (typeof value === 'string') {
      const pin = pins.find(item => item.key.length === keys.length && item.key.every((key, i) => key === keys[i]));
      if (pin) {
        if (from !== pin.source)
          throw new Error(
            `Traditional Chinese pin for ${path} ${keys.join('.')} was written for another source; update or remove it in ${OVERRIDES}`
          );
        used.add(pin);
        return pin.text;
      }
      let text = value;
      for (const rule of rules.context) {
        if (rule.keys && !(keys.length && new RegExp(rule.keys).test(keys.join('.')))) continue;
        if (typeof en === 'string') {
          if (rule.keepWhenEnglish && new RegExp(rule.keepWhenEnglish, 'i').test(en)) continue;
          if (rule.whenEnglish && !new RegExp(rule.whenEnglish, 'i').test(en)) continue;
        } else if (!rule.keys && !rule.applyWithoutEnglish) continue;
        text = text.split(rule.from).join(rule.to);
      }
      return text;
    }
    if (Array.isArray(value)) return value.map((item, i) => walk(from?.[i], en?.[i], item, [...keys, String(i)]));
    if (value && typeof value === 'object')
      return Object.fromEntries(
        Object.entries(value).map(([key, item]) => [key, walk(from?.[key], en?.[key], item, [...keys, key])])
      );
    return value;
  };
  const result = walk(source, english, converted, []);
  for (const pin of pins)
    if (!used.has(pin))
      throw new Error(
        `Traditional Chinese pin for ${path} ${pin.key.join('.')} matches no string; remove it from ${OVERRIDES}`
      );
  return result;
}

export function generateTraditional(check = false) {
  const inputs = {};
  const english = {};
  for (const name of ['web', 'desktop', 'shared', 'native']) {
    const target = loadTarget(name);
    inputs[target.path('zh-Hant')] = target.source;
    english[target.path('zh-Hant')] = target.english;
  }
  const directory = 'apps/desktop/release-notes';
  for (const version of readdirSync(directory)) {
    const source = `${directory}/${version}/zh-CN.md`;
    // Historical six-language releases need no new files; already existing
    // Traditional notes and every release after 0.1.16 are derived.
    const parts = version.split('.').map(Number);
    const current = parts[0] > 0 || parts[1] > 1 || parts[2] > 16;
    if (existsSync(source) && (current || existsSync(`${directory}/${version}/zh-Hant.md`)))
      inputs[`${directory}/${version}/zh-Hant.md`] = readFileSync(source, 'utf8');
  }
  const rules = JSON.parse(readFileSync(TERMS, 'utf8'));
  const pins = JSON.parse(readFileSync(OVERRIDES, 'utf8'));
  for (const path of Object.keys(pins))
    if (path !== 'about' && !(path in inputs))
      throw new Error(`${OVERRIDES} names a file that is not derived: ${path}`);
  const outputs = convertTraditional(inputs);
  for (const [path, converted] of Object.entries(outputs)) {
    const value = refineTraditional(path, inputs[path], english[path], converted, rules, pins[path]);
    const text = typeof value === 'string' ? value : JSON.stringify(value, null, 2) + '\n';
    if (existsSync(path) && readFileSync(path, 'utf8') === text) continue;
    if (check)
      throw new Error(
        `Derived Traditional Chinese is outdated: ${path}; run node tooling/scripts/derive-traditional.mjs`
      );
    writeFileSync(path, text);
  }
}
if (process.argv[1] && import.meta.url === pathToFileURL(resolve(process.argv[1])).href)
  generateTraditional(process.argv.includes('--check'));
