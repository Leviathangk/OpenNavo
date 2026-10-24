import assert from 'node:assert/strict';
import { createRequire } from 'node:module';
import test from 'node:test';
import { convertTraditional } from './traditional-converter.mjs';

const require = createRequire(new URL('../../packages/ui/package.json', import.meta.url));
const MarkdownIt = require('markdown-it');
const markdown = new MarkdownIt({ html: false });
const cases = [
  '> ~~~sh\n> echo 软件\n> ~~~\n',
  '- 软件\n\n    ~~~sh\n    echo 软件\n    ~~~\n',
  '> ````markdown\n> ```sh\n> echo 软件\n> ```\n> ````\n',
  '1. 软件\n\n   > ~~~sh\n   > echo 软件\n   > ~~~\n',
  '- 软件\n\n      echo 软件\n',
  '> ~~~sh\n> echo 软件\n',
  '- > ````\n  > ```\n  > echo 软件\n  > ```\n  > ````\n',
  '> - 软件\n>\n>   ~~~\n>   echo 软件\n>   ~~~\n'
];

test('conversion preserves the code tokens recognized by the product Markdown parser', () => {
  const source = Object.fromEntries(cases.map((value, i) => [String(i), `${value}\n软件`]));
  const converted = convertTraditional(source);
  const code = text =>
    markdown
      .parse(text, {})
      .filter(token => ['fence', 'code_block'].includes(token.type))
      .map(token => token.content);
  for (const [key, value] of Object.entries(source)) {
    assert.ok(
      code(value).some(text => text.includes('echo 软件')),
      value
    );
    assert.deepEqual(code(converted[key]), code(value), value);
    assert.ok(converted[key].endsWith('\n軟體'), converted[key]);
  }
});

test('fences, inline code and link targets survive conversion without hiding prose', () => {
  const cases = [
    '```sh\necho 软件\n```\n\n软件 `软件.json`',
    '``软件 `echo 软件` 文件`` 软件',
    '软件 [下载](https://example.test/a(软件)/软件.txt)',
    '软件 [下载](./a(软件)/软件.txt)',
    '软件 [下载](<./软件(文件).txt>)',
    '软件 [下载][资源]\n\n[资源]: ./软件.txt',
    '软件 [软件][]\n\n[软件]: ./软件.txt',
    '软件 [软件]\n\n[软件]: ./软件.txt',
    '软件 ![下载][资源]\n\n[资源]: ./软件.txt',
    '> ```sh\n> echo 软件\n> ```\n\n软件 `软件.json`',
    '``\n软件 `echo 软件`\n`` 软件',
    '软件 [下载](./软件\\(文件\\).txt)',
    '软件 [下载][资源]\n\n[资源]:\n  ./软件.txt "文件标题"'
  ];
  const source = Object.fromEntries(cases.map((value, i) => [String(i), value]));
  const converted = convertTraditional(source);
  const literals = text => {
    const result = [];
    const visit = tokens => {
      for (const token of tokens) {
        if (['fence', 'code_block', 'code_inline'].includes(token.type)) result.push([token.type, token.content]);
        if (token.type === 'link_open') result.push(['href', token.attrGet('href')]);
        if (token.type === 'image') result.push(['src', token.attrGet('src')]);
        if (token.children) visit(token.children);
      }
    };
    visit(markdown.parse(text, {}));
    return result;
  };
  for (const [key, value] of Object.entries(source)) {
    assert.ok(literals(value).length, value);
    assert.deepEqual(literals(converted[key]), literals(value), value);
    assert.ok(converted[key].includes('軟體'), converted[key]);
  }
  assert.equal(converted[0], '```sh\necho 软件\n```\n\n軟體 `软件.json`');
  assert.equal(converted[1], '``软件 `echo 软件` 文件`` 軟體');
});
