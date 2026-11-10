// Inspect templates with the Vue AST and scripts with the TS AST to exclude comments and dynamic interpolation from copy checks.
import { createRequire } from 'node:module';
import { readFileSync, readdirSync } from 'node:fs';
import { resolve, relative } from 'node:path';
import ts from 'typescript';
const require = createRequire(new URL('../../apps/web/package.json', import.meta.url));
const { parse, compileTemplate } = require('@vue/compiler-sfc');
const roots = ['apps/web/app', 'apps/desktop/src', 'packages/ui/src', 'apps/admin/src'];
const technical = new Set([
  'OpenNavo',
  'Homebrew',
  'SHA-256',
  'Brewfile',
  'Token',
  'Cask',
  'App',
  'macOS',
  'GitHub',
  'URL',
  'API',
  'JSON',
  'Markdown',
  'CSV',
  'ETag',
  'ARM',
  'Intel',
  'Apple Silicon',
  'aarch64',
  'x86_64',
  'arm64',
  'x64',
  '⌘K',
  '⌘ K',
  'Hermes',
  'KB',
  'chars',
  '⌘Y'
]);
// Hermes is an isolated assistant surface with its own complete bilingual catalog
// and user-facing runtime notices. It is audited by check-interface-i18n and the
// Hermes tests; keep this exception scoped to that module rather than exempting
// desktop copy generally.
const hermesCopyAllowlist = new Set([
  'apps/desktop/src/hermes/content.ts',
  'apps/desktop/src/stores/hermes.ts',
  'apps/desktop/src/ipc/mock.ts'
]);

const attributes = new Set(['title', 'placeholder', 'aria-label', 'alt', 'label', 'description', 'subtitle', 'hint']);
const findings = [];
function report(file, offset, text) {
  const line = readFileSync(file, 'utf8').slice(0, offset).split('\n').length;
  findings.push(`${relative(process.cwd(), file)}:${line}: ${text.trim().slice(0, 100)}`);
}
function isCopy(text) {
  return /\p{L}/u.test(text) && !technical.has(text.trim()) && !/^https?:\/\/[^\s]*$/.test(text.trim());
}
function script(file, content, offset = 0) {
  const ast = ts.createSourceFile(
    file,
    content,
    ts.ScriptTarget.Latest,
    true,
    file.endsWith('.tsx') ? ts.ScriptKind.TSX : ts.ScriptKind.TS
  );
  function visit(node) {
    if (
      (ts.isStringLiteral(node) ||
        ts.isNoSubstitutionTemplateLiteral(node) ||
        ts.isTemplateHead(node) ||
        ts.isTemplateMiddle(node) ||
        ts.isTemplateTail(node)) &&
      /[\p{Script=Han}\p{Script=Hiragana}\p{Script=Katakana}]/u.test(node.text)
    ) {
      let parent = node.parent;
      let developer = false;
      while (parent) {
        if (
          (ts.isCallExpression(parent) && parent.expression.getText(ast).startsWith('console.')) ||
          (ts.isNewExpression(parent) && /^(Error|TypeError|RangeError)$/.test(parent.expression.getText(ast)))
        )
          developer = true;
        parent = parent.parent;
      }
      if (!developer) report(file, offset + node.getStart(ast), node.text);
    }
    ts.forEachChild(node, visit);
  }
  visit(ast);
}
function check(file) {
  const content = readFileSync(file, 'utf8');
  if (!file.endsWith('.vue')) {
    script(file, content);
    return;
  }
  const { descriptor, errors } = parse(content, { filename: file });
  if (errors.length) throw new Error(`SFC parse failed: ${file}`);
  for (const block of [descriptor.script, descriptor.scriptSetup])
    if (block) script(file, block.content, block.loc.start.offset);
  if (!descriptor.template) return;
  const block = descriptor.template;
  const compiled = compileTemplate({ source: block.content, filename: file, id: file });
  if (compiled.errors.length) throw new Error(`Template parse failed: ${file}`);
  function visit(node) {
    if (node.type === 2 && isCopy(node.content))
      report(file, block.loc.start.offset + node.loc.start.offset, node.content);
    for (const prop of node.props ?? []) {
      if (prop.type === 6 && attributes.has(prop.name) && prop.value && isCopy(prop.value.content))
        report(file, block.loc.start.offset + prop.loc.start.offset, prop.value.content);
    }
    if (node.type === 12) visit(node.content);
    for (const child of node.children ?? []) if (typeof child === 'object') visit(child);
    for (const branch of node.branches ?? []) visit(branch);
  }
  visit(compiled.ast);
}
function walk(directory) {
  for (const entry of readdirSync(directory, { withFileTypes: true })) {
    const path = resolve(directory, entry.name);
    if (entry.isDirectory()) {
      if (!['langs', 'tests', '__tests__', 'typings'].includes(entry.name)) walk(path);
    } else if (
      /\.(vue|ts|tsx)$/.test(path) &&
      !/\.(gen|test|spec)\./.test(path) &&
      !path.endsWith('/ipc/bindings.ts') &&
      !/\/i18n\/(?:zh-CN|en-US)\.ts$/.test(path) &&
      !hermesCopyAllowlist.has(relative(process.cwd(), path))
    )
      check(path);
  }
}
for (const path of process.argv.slice(2).length ? process.argv.slice(2) : roots) walk(resolve(path));
if (findings.length) {
  console.error(findings.join('\n'));
  process.exit(1);
}
console.log('Hardcoded interface copy check passed.');
