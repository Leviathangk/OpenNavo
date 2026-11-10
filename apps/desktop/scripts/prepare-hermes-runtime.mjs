#!/usr/bin/env node
/**
 * Build the relocatable Hermes sidecar used by the OpenNavo desktop app.
 *
 * The app ships a standalone CPython plus hermes-agent in Resources/hermes. The
 * generated directories are large and are intentionally ignored by git; run
 * this before a release build:
 *
 *   pnpm --filter @opennavo/desktop hermes:prepare
 *
 * CI can provide HERMES_RUNTIME_PREBUILT pointing at a previously built
 * resources/hermes directory to skip the download.
 */
import { execFileSync } from 'node:child_process';
import { chmodSync, cpSync, existsSync, mkdirSync, rmSync, writeFileSync } from 'node:fs';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

const HERMES_VERSION = '0.23.0';
const PYTHON_VERSION = '3.14';
const here = dirname(fileURLToPath(import.meta.url));
const desktop = resolve(here, '..');
const resources = join(desktop, 'src-tauri', 'resources', 'hermes');
const pythonDir = join(resources, 'python');
const siteDir = join(resources, 'site');
const binDir = join(resources, 'bin');
const force = process.argv.includes('--force');
const prebuilt = process.env.HERMES_RUNTIME_PREBUILT;

function run(command, args, options = {}) {
  process.stdout.write(`\n$ ${command} ${args.join(' ')}\n`);
  execFileSync(command, args, { stdio: 'inherit', ...options });
}

function uvPath() {
  try {
    execFileSync('uv', ['--version'], { stdio: 'ignore' });
    return 'uv';
  } catch {
    throw new Error('uv is required to build the Hermes runtime (brew install uv).');
  }
}

function installRuntime() {
  if (prebuilt) {
    const source = resolve(prebuilt);
    if (!existsSync(join(source, 'bin', 'hermes')))
      throw new Error(`HERMES_RUNTIME_PREBUILT is not a Hermes runtime: ${source}`);
    rmSync(resources, { recursive: true, force: true });
    mkdirSync(dirname(resources), { recursive: true });
    cpSync(source, resources, { recursive: true, dereference: true });
    console.log(`Copied prebuilt Hermes runtime from ${source}`);
    return;
  }

  const uv = uvPath();
  if (!force && existsSync(join(siteDir, 'hermes_cli', 'main.py'))) {
    const cuaApp = process.env.HERMES_CUA_DRIVER_APP || '/Applications/CuaDriver.app';
    if (!existsSync(join(resources, 'CuaDriver.app')) && existsSync(cuaApp)) {
      cpSync(cuaApp, join(resources, 'CuaDriver.app'), { recursive: true, dereference: true });
      console.log('Added CuaDriver.app to the existing Hermes runtime.');
    }
    console.log('Hermes runtime already exists; pass --force to rebuild.');
    return;
  }

  rmSync(resources, { recursive: true, force: true });
  mkdirSync(resources, { recursive: true });
  mkdirSync(binDir, { recursive: true });

  run(uv, ['python', 'install', PYTHON_VERSION]);
  const python = execFileSync(uv, ['python', 'find', PYTHON_VERSION], { encoding: 'utf8' }).trim();
  if (!python || !existsSync(python)) throw new Error(`uv did not return a Python ${PYTHON_VERSION} interpreter`);

  console.log(`\nCopying standalone Python from ${python}`);
  cpSync(dirname(dirname(python)), pythonDir, { recursive: true, dereference: true });

  // 0.23 is not on PyPI yet. Build from the tagged source with the
  // repository's uv.lock so provider plugins and exact pins stay consistent.
  const tmp = join(desktop, 'node_modules', '.cache', `hermes-${HERMES_VERSION}`);
  rmSync(tmp, { recursive: true, force: true });
  mkdirSync(tmp, { recursive: true });
  const tarball = join(tmp, `hermes-${HERMES_VERSION}.tar.gz`);
  run('curl', [
    '-sSL',
    '--fail',
    `https://github.com/NousResearch/hermes-agent/archive/refs/tags/v${HERMES_VERSION}.tar.gz`,
    '-o',
    tarball
  ]);
  run('tar', ['-xzf', tarball, '-C', tmp]);
  const source = join(tmp, `hermes-agent-${HERMES_VERSION}`);
  const venv = join(tmp, 'venv');
  run(uv, ['sync', '--frozen', '--python', join(pythonDir, 'bin', 'python3.14')], {
    cwd: source,
    env: { ...process.env, UV_PROJECT_ENVIRONMENT: venv }
  });
  run(uv, ['pip', 'install', '--python', join(venv, 'bin', 'python3.14'), '--no-deps', '--force-reinstall', source], {
    cwd: source,
    env: { ...process.env, HERMES_NIX_BUILD: '1' }
  });
  const sitePackages = join(venv, 'lib', 'python3.14', 'site-packages');
  cpSync(sitePackages, siteDir, { recursive: true, dereference: true });
  cpSync(join(source, 'uv.lock'), join(siteDir, 'uv.lock'));
  rmSync(join(siteDir, '__editable__hermes_agent-0.0.0.pth'), { force: true });
  rmSync(join(siteDir, '__editable___hermes_agent_0_0_0_finder.py'), { force: true });

  const wrapper = `#!/bin/sh
set -eu
DIR=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
export PYTHONPATH="$DIR/site\${PYTHONPATH:+:$PYTHONPATH}"
exec "$DIR/python/bin/python3.14" -c 'from hermes_cli.main import main; main()' "$@"
`;
  writeFileSync(join(binDir, 'hermes'), wrapper, { mode: 0o755 });
  chmodSync(join(binDir, 'hermes'), 0o755);

  const cuaApp = process.env.HERMES_CUA_DRIVER_APP || '/Applications/CuaDriver.app';
  if (existsSync(cuaApp)) {
    cpSync(cuaApp, join(resources, 'CuaDriver.app'), { recursive: true, dereference: true });
    console.log('Bundled CuaDriver.app; grant Accessibility and Screen Recording on first use.');
  } else {
    console.warn('CuaDriver.app was not found; computer_use will be unavailable until it is bundled.');
  }

  // The 0.23 provider catalog ships several providers as plugins. Copilot
  // moved out of core; install the pinned community plugin into the runtime.
  try {
    const buildHome = join(resources, 'build-home');
    run(join(binDir, 'hermes'), ['plugins', 'install', 'github-copilot'], {
      cwd: resources,
      env: { ...process.env, HERMES_HOME: buildHome }
    });
    const bundledPlugin = join(buildHome, 'plugins', 'github-copilot');
    if (existsSync(bundledPlugin)) {
      mkdirSync(join(resources, 'plugins'), { recursive: true });
      cpSync(bundledPlugin, join(resources, 'plugins', 'github-copilot'), { recursive: true, dereference: true });
    }
    rmSync(buildHome, { recursive: true, force: true });
  } catch (error) {
    console.warn('Could not preinstall github-copilot plugin:', error.message);
  }

  writeFileSync(
    join(resources, 'manifest.json'),
    JSON.stringify({ hermesAgent: HERMES_VERSION, python: PYTHON_VERSION, builtAt: new Date().toISOString() }, null, 2)
  );
  console.log(`\nHermes runtime ready at ${resources}`);
}

installRuntime();

// Verify the generated wrapper without needing model credentials.
const wrapper = join(binDir, 'hermes');
if (existsSync(wrapper)) {
  try {
    const output = execFileSync(wrapper, ['--version'], { encoding: 'utf8' });
    console.log(output.split('\n').slice(0, 3).join('\n'));
  } catch (error) {
    console.error('Hermes runtime verification failed:', error.message);
    process.exitCode = 1;
  }
}
