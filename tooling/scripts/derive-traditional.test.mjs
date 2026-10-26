import assert from 'node:assert/strict';
import test from 'node:test';
import { refineTraditional } from './derive-traditional.mjs';

const rules = {
  context: [
    { from: '專案', to: '項目', keepWhenEnglish: 'project' },
    { from: '通用', to: '一般', whenEnglish: '^\\s*general\\s*$' }
  ]
};

// Unconditional replacements run in the Go converter (internal/traditional, TestWordingRules).
test('a terms file that still has replace rules fails instead of being ignored', () => {
  assert.throws(() => refineTraditional('x.json', {}, {}, {}, { replace: [], context: [] }), /opennavo-terms\.json/);
});

test('context rules follow the English source and leave text without English as converted', () => {
  const converted = { item: '垃圾桶裡的專案', project: '專案檔案', none: '專案', general: '通用', universal: '通用' };
  const english = { item: 'Items in the Trash', project: 'Project files', general: 'General', universal: 'Universal' };
  assert.deepEqual(refineTraditional('x.json', converted, english, converted, rules), {
    item: '垃圾桶裡的項目',
    project: '專案檔案',
    none: '專案',
    general: '一般',
    universal: '通用'
  });
});

test('pins replace one string and fail once their Simplified source changes or disappears', () => {
  const pin = { key: ['firewall', 'allow'], source: '允许这个地址', text: '允許這個位址' };
  const converted = { firewall: { allow: '允許這個地址' } };
  assert.deepEqual(refineTraditional('x.json', { firewall: { allow: '允许这个地址' } }, {}, converted, rules, [pin]), {
    firewall: { allow: '允許這個位址' }
  });
  assert.throws(
    () => refineTraditional('x.json', { firewall: { allow: '允许这个网址' } }, {}, converted, rules, [pin]),
    /another source/
  );
  assert.throws(
    () => refineTraditional('x.json', { other: '地址' }, {}, { other: '地址' }, rules, [pin]),
    /matches no string/
  );
});

test('key-scoped rules and rules without English follow their conditions', () => {
  const scoped = {
    context: [
      { from: '地址', to: '位址', keys: '^firewall\\.' },
      { from: '地址', to: '網址', keepWhenEnglish: 'e-?mail', applyWithoutEnglish: true },
      { from: '後臺', to: '背景', keepWhenEnglish: '\\badmin\\b', applyWithoutEnglish: true }
    ]
  };
  const converted = { firewall: { any: '任何地址' }, download: '下載地址', mail: '郵件地址', admin: '管理後臺' };
  const english = {
    firewall: { any: 'Any address' },
    download: 'Download URL',
    mail: 'E-mail address',
    admin: 'Admin console'
  };
  assert.deepEqual(refineTraditional('x.json', converted, english, converted, scoped), {
    firewall: { any: '任何位址' },
    download: '下載網址',
    mail: '郵件地址',
    admin: '管理後臺'
  });
  assert.equal(refineTraditional('notes.md', '後臺下載地址', undefined, '後臺下載地址', scoped), '背景下載網址');
});

test('release notes receive the context rules that apply without English', () => {
  const background = {
    context: [{ from: '後臺', to: '背景', keepWhenEnglish: '\\badmin\\b', applyWithoutEnglish: true }]
  };
  assert.equal(refineTraditional('notes.md', '需要後臺權限', undefined, '需要後臺權限', background), '需要背景權限');
});
