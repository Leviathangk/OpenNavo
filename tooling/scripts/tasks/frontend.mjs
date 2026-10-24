// Frontend tasks (frontend-maintained). Dispatch lint / typecheck / test / build / gen scripts from each package.json.
import { spawn, spawnSync } from 'node:child_process';
import { run } from './shared.mjs';

// Dispatch only project packages, excluding the admin template's @sa/* packages.
const workspace = ['--filter', '@opennavo/*', '--if-present', 'run'];

// Desktop Rust tasks (06 section 16, 09 section 4.4).
const tauriDirectory = 'apps/desktop/src-tauri';

// Static Prism mocks (04 section 7.2): avoid --dynamic, which randomizes business status codes.
const mocks = [
  { spec: 'apps/server/api/public.openapi.yaml', port: '4010' },
  { spec: 'apps/server/api/admin.openapi.yaml', port: '4011' }
];

export const tasks = new Set([
  'lint',
  'test',
  'build',
  'gen',
  'dev-web',
  'dev-admin',
  'dev-desktop',
  'dev-desktop-web',
  'mock',
  'e2e'
]);

/** Check dedicated E2E API readiness synchronously so the e2e task can decide whether to run web tests. */
function e2eStackReady() {
  const probe = spawnSync(
    'node',
    ['-e', "fetch('http://127.0.0.1:18082/readyz').then(r => process.exit(r.ok ? 0 : 1), () => process.exit(1))"],
    { stdio: 'ignore', timeout: 5000 }
  );
  return probe.status === 0;
}

/** Run multiple persistent processes; stop all when any exits or on Ctrl+C. */
function runTogether(commands) {
  const children = commands.map(({ command, args }) => spawn(command, args, { stdio: 'inherit' }));
  const stop = () => children.forEach(child => child.exitCode === null && child.kill('SIGTERM'));
  process.on('SIGINT', stop);
  process.on('SIGTERM', stop);
  children.forEach(child =>
    child.on('exit', code => {
      stop();
      process.exitCode = code ?? 1;
    })
  );
}

export function runTask(task) {
  switch (task) {
    case 'lint':
      run('node', ['tooling/scripts/derive-traditional.mjs', '--check']);
      run('node', ['--test', 'tooling/scripts/traditional-markdown.test.mjs']);
      run('node', ['tooling/scripts/check-interface-i18n.mjs']);
      run('node', ['tooling/scripts/check-hardcoded-i18n.mjs']);
      run('node', ['tooling/scripts/sync-brand-readme.mjs', '--check']);
      run('pnpm', ['run', 'format:check']);
      run('pnpm', [...workspace, 'lint']);
      run('pnpm', [...workspace, 'typecheck']);
      run('cargo', ['fmt', '--check'], tauriDirectory);
      run('cargo', ['clippy', '--all-targets', '--', '-D', 'warnings'], tauriDirectory);
      return;
    case 'test':
      run('node', [
        '--test',
        'tooling/scripts/check-hardcoded-i18n.test.mjs',
        'tooling/scripts/sync-brand-readme.test.mjs',
        'apps/web/scripts/og-font.test.mjs'
      ]);
      run('pnpm', [...workspace, 'test']);
      run('cargo', ['test'], tauriDirectory);
      return;
    case 'build':
    case 'gen':
      run('node', ['tooling/scripts/derive-traditional.mjs']);
      run('pnpm', [...workspace, task]);
      return;
    case 'mock':
      runTogether(
        mocks.map(({ spec, port }) => ({
          command: 'pnpm',
          args: ['exec', 'prism', 'mock', spec, '-h', '127.0.0.1', '-p', port]
        }))
      );
      return;
    case 'dev-web':
      // MOCK=1 uses the public Prism mock (4010); otherwise use the local backend (8080).
      run('pnpm', ['--filter', '@opennavo/web', 'run', process.env.MOCK ? 'dev:mock' : 'dev']);
      return;
    case 'e2e':
      // Admin menu/permission tests intercept APIs and need no backend; full-stack web tests require make e2e-up (03 section 5.5).
      run('pnpm', ['--filter', '@opennavo/admin', 'run', 'e2e']);
      if (e2eStackReady()) run('pnpm', ['--filter', '@opennavo/web', 'run', 'e2e']);
      else
        console.log(
          'E2E stack is not running (127.0.0.1:18082); skipping full-stack web tests. Run make e2e-up first.'
        );
      return;
    case 'dev-desktop':
      run('pnpm', ['--filter', '@opennavo/desktop', 'tauri', 'dev']);
      return;
    case 'dev-desktop-web':
      // Run the desktop UI in a regular browser with IPC mocked by src/ipc/mock.ts.
      run('pnpm', ['--filter', '@opennavo/desktop', 'run', 'dev:web']);
      return;
    case 'dev-admin':
      // MOCK=1 uses the admin Prism mock (4011); otherwise use the local backend (8080).
      run('pnpm', ['--filter', '@opennavo/admin', 'run', process.env.MOCK ? 'dev:mock' : 'dev']);
      return;
    default:
      console.log(
        `Frontend task ${task} is not wired yet (implemented by the corresponding scaffold task in the roadmap).`
      );
  }
}
