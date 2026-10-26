import { execFileSync, spawnSync } from 'node:child_process';
import { mkdtempSync, readFileSync, rmSync, mkdirSync, copyFileSync, existsSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';

// Pin the same scanner locally and in CI; Go caches it after the first run.
const scanner = 'github.com/zricethezav/gitleaks/v8@v8.24.3';
const config = fileURLToPath(new URL('../config/.gitleaks.toml', import.meta.url));
const mode = process.argv[2] ?? '--staged';
if (!['--staged', '--worktree', '--history'].includes(mode) || process.argv.length > 3) {
  console.error('Usage: node tooling/scripts/check-secrets.mjs [--staged|--worktree|--history]');
  process.exit(2);
}
const git = args => execFileSync('git', args, { encoding: 'utf8', maxBuffer: 32 * 1024 * 1024 });
const paths = git(['ls-files', '-z', ...(mode === '--worktree' ? ['--cached', '--others', '--exclude-standard'] : [])])
  .split('\0')
  .filter(Boolean);
let failed = false;
for (const path of new Set(paths)) {
  if (
    /(^|\/)\.env(\.|$)/.test(path) &&
    !path.endsWith('.example') &&
    !/^apps\/admin\/\.env(?:\.(?:test|prod))?$/.test(path)
  ) {
    console.error(`Forbidden environment file: ${path}`);
    failed = true;
  }
}
const temp = mkdtempSync(join(tmpdir(), 'opennavo-secrets-'));
try {
  let target = process.cwd();
  if (mode !== '--history') {
    target = join(temp, 'source');
    mkdirSync(target);
    if (mode === '--staged') {
      git(['checkout-index', '--all', `--prefix=${target}/`]);
    } else {
      for (const path of new Set(paths)) {
        mkdirSync(dirname(join(target, path)), { recursive: true });
        try {
          copyFileSync(path, join(target, path));
        } catch (error) {
          if (error.code !== 'ENOENT') throw error;
        }
      }
    }
  }
  // The i18n lock may only hold SHA-256 source hashes. Gitleaks exempts lowercase hex there even
  // when a chunk boundary hides the line, so any other value must fail before that exemption.
  const lock = join(target, 'tooling/i18n/i18n.lock.json');
  if (existsSync(lock)) {
    const walk = (value, path) => {
      if (typeof value === 'string' && !/^[a-f0-9]{64}$/.test(value)) {
        console.error(`tooling/i18n/i18n.lock.json: ${path} is not a SHA-256 source hash`);
        failed = true;
      } else if (value && typeof value === 'object') {
        for (const [key, item] of Object.entries(value)) walk(item, `${path}/${key}`);
      }
    };
    try {
      walk(JSON.parse(readFileSync(lock, 'utf8')), '');
    } catch {
      console.error('tooling/i18n/i18n.lock.json: not valid JSON');
      failed = true;
    }
  }
  const report = join(temp, 'report.json');
  const result = spawnSync(
    'go',
    [
      'run',
      scanner,
      mode === '--history' ? 'git' : 'dir',
      '.',
      ...(mode === '--history' ? ['--log-opts=--all'] : []),
      '--config',
      config,
      '--ignore-gitleaks-allow',
      '--redact',
      '--no-banner',
      '--log-level',
      'error',
      '--report-format',
      'json',
      '--report-path',
      report
    ],
    { cwd: target, encoding: 'utf8', maxBuffer: 32 * 1024 * 1024 }
  );
  if (result.error || ![0, 1].includes(result.status)) {
    throw new Error('Gitleaks could not run. Check Go installation and module download access.');
  }
  let findings;
  try {
    findings = JSON.parse(readFileSync(report, 'utf8'));
  } catch {
    throw new Error('Gitleaks did not produce a valid report. Check Go and network access.');
  }
  for (const item of findings) {
    // Never print secret values or source snippets, even for failed scans.
    console.error(`${item.File}:${item.StartLine}: ${item.RuleID}`);
  }
  if (result.status !== 0 || findings.length) failed = true;
  if (!failed) console.log(`Secret scan passed (${mode.slice(2)}).`);
} finally {
  rmSync(temp, { recursive: true, force: true });
}
process.exitCode = failed ? 1 : 0;
