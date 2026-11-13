// Backend tasks (backend-maintained), including apps/server/, ops/, and MCP commands.
import { existsSync, readFileSync } from 'node:fs';
import { pathToFileURL } from 'node:url';
import { developmentEnvironment, run } from './shared.mjs';

const hasServer = () => existsSync('apps/server/go.mod');

export const tasks = new Set([
  'gen-mcp',
  'lint-mcp',
  'test-mcp',
  'build-mcp',
  'dev-mcp',
  'i18n-sync',
  'lint',
  'test',
  'build',
  'gen',
  'infra',
  'verify-infra',
  'catalog-sync',
  'analytics-sync',
  'tap-import',
  'cask-verify',
  'search-verify',
  'llm-smoke',
  'snapshot-build',
  'desktop-fixtures',
  'api-performance',
  'desktop-verify',
  'alerts-test',
  'deploy-lint',
  'prod-verify',
  'migrate',
  'seed',
  'seed-demo',
  'seed-e2e',
  'e2e-up',
  'e2e-down',
  'e2e-reset',
  'dev-api',
  'dev-worker'
]);

export function runTask(task) {
  if (task.endsWith('-mcp')) {
    runMCP(task.slice(0, -4));
    return;
  }
  switch (task) {
    case 'i18n-sync':
      run('node', ['tooling/scripts/i18n-sync.mjs', ...process.argv.slice(3)]);
      return;
    case 'api-performance':
      run(
        'go',
        [
          'test',
          '-count=1',
          '-tags=integration,performance',
          '-run',
          '^TestPublicAPILatencyBaseline$',
          '-v',
          './internal/http'
        ],
        'apps/server'
      );
      return;
    case 'e2e-up':
    case 'e2e-down':
    case 'e2e-reset':
      run('python3', ['-I', 'ops/e2e.py', task.slice(4), ...process.argv.slice(3)]);
      return;
    case 'desktop-fixtures':
      run(
        'go',
        [
          'test',
          '-count=1',
          '-tags=integration',
          '-run',
          '^TestDesktopFixturesMatchSeedAndProductionSerialization$',
          './internal/service/e2e'
        ],
        'apps/server',
        { ...process.env, UPDATE_DESKTOP_FIXTURES: '1' }
      );
      return;
    case 'lint':
      run('node', ['tooling/scripts/check-interface-i18n.mjs', '--targets', 'native,server']);
      run('node', ['tooling/scripts/gen-locales.mjs', '--check']);
      run('pnpm', [
        'exec',
        'redocly',
        'lint',
        'apps/server/api/public.openapi.yaml',
        'apps/server/api/public-external.openapi.yaml',
        'apps/server/api/admin.openapi.yaml',
        '--config',
        'apps/server/api/redocly.yaml'
      ]);
      if (hasServer()) run('go', ['tool', 'golangci-lint', 'run'], 'apps/server');
      return;
    case 'test':
      run('node', ['--test', 'tooling/scripts/interface-i18n.test.mjs', 'tooling/scripts/tasks/shared.test.mjs']);
      if (hasServer()) run('go', ['test', '-race', './...'], 'apps/server');
      return;
    case 'build':
      if (hasServer()) run('go', ['build', './...'], 'apps/server');
      return;
    case 'gen':
      run('node', ['tooling/scripts/gen-locales.mjs']);
      if (hasServer()) {
        for (const api of ['public', 'admin'])
          run('go', ['tool', 'oapi-codegen', '-config', `api/oapi-${api}.yaml`, `api/${api}.openapi.yaml`], 'apps/server');
      }
      return;
    case 'infra':
      run('docker', ['compose', '-f', 'ops/docker-compose.dev.yml', 'up', '-d', '--wait']);
      run('docker', ['compose', '-f', 'ops/docker-compose.dev.yml', 'run', '--rm', 'storage-init']);
      return;
    case 'verify-infra':
      run('go', ['run', './cmd/storage-verify'], 'apps/server', developmentEnvironment());
      return;
    case 'catalog-sync':
      run('go', ['run', './cmd/catalog-sync'], 'apps/server', developmentEnvironment());
      return;
    case 'tap-import':
      // Registers every cask a third-party tap lists; flags come from `make tap-import ARGS="-tap owner/tap"`.
      run('go', ['run', './cmd/tap-import', ...process.argv.slice(3)], 'apps/server', developmentEnvironment());
      return;
    case 'cask-verify':
      // Checks one cask file the way the tap sync reads it; no database or configuration is needed.
      run('go', ['run', './cmd/cask-verify', ...process.argv.slice(3)], 'apps/server');
      return;
    case 'analytics-sync':
      run('go', ['run', './cmd/analytics-sync'], 'apps/server', developmentEnvironment());
      return;
    case 'search-verify':
      run('go', ['run', './cmd/search-verify'], 'apps/server', developmentEnvironment());
      return;
    case 'llm-smoke':
      run(
        'go',
        ['test', '-count=1', '-tags=live', '-run', '^TestLiveGatewaySmoke$', '-v', './internal/llm/openai'],
        'apps/server',
        { ...developmentEnvironment(), OPENNAVO_LIVE_LLM: '1' }
      );
      return;
    case 'snapshot-build':
      run('go', ['run', './cmd/snapshot-build', ...process.argv.slice(3)], 'apps/server', developmentEnvironment());
      return;
    case 'desktop-verify':
      run('go', ['run', './cmd/desktop-verify'], 'apps/server', developmentEnvironment());
      return;
    case 'alerts-test':
      run('docker', [
        'run',
        '--rm',
        '-v',
        `${process.cwd()}/ops/prometheus:/etc/prometheus:ro`,
        '--entrypoint',
        '/bin/promtool',
        'registry.hub.docker.com/prom/prometheus:v3.15.0',
        'test',
        'rules',
        '/etc/prometheus/rules.test.yml'
      ]);
      return;
    case 'deploy-lint':
      run('python3', ['-I', 'ops/lint.py']);
      run('python3', ['-I', 'ops/test_github_deploy.py']);
      return;
    case 'prod-verify':
      run('python3', ['-I', 'ops/verify-production.py', ...process.argv.slice(3)]);
      return;
    case 'migrate':
      run('go', ['run', './cmd/migrate', 'up'], 'apps/server', developmentEnvironment());
      return;
    case 'seed':
      run('go', ['run', './cmd/seed'], 'apps/server', developmentEnvironment());
      return;
    case 'seed-demo': {
      // Use the fixed local Compose service, never a database address from runtime configuration.
      const compose = ['compose', '-p', 'opennavo-dev', '-f', 'ops/docker-compose.dev.yml', 'exec', '-T'];
      run('docker', [
        ...compose,
        'postgres',
        'psql',
        '-X',
        '-v',
        'ON_ERROR_STOP=1',
        '-U',
        'opennavo',
        '-d',
        'opennavo',
        '-c',
        readFileSync('apps/server/seeds/demo-content.sql', 'utf8')
      ]);
      run('docker', [
        ...compose,
        'redis',
        'redis-cli',
        'PUBLISH',
        'cache:invalidate',
        JSON.stringify({ patterns: ['c:home:*'] })
      ]);
      return;
    }
    case 'seed-e2e':
      run('go', ['run', './cmd/seed-e2e'], 'apps/server', developmentEnvironment());
      return;
    case 'dev-api':
      run('go', ['run', './cmd/api'], 'apps/server', developmentEnvironment());
      return;
    case 'dev-worker':
      if (!existsSync('apps/server/cmd/worker')) {
        console.log('Worker is not implemented yet (M1-04).');
        return;
      }
      run('go', ['run', './cmd/worker'], 'apps/server', developmentEnvironment());
      return;
    default:
      throw new Error(`Unsupported backend task: ${task}`);
  }
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) runTask(process.argv[2]);

// Keep -server commands limited to Go; the main entry point also runs MCP so CI can cache Python independently.
export function runMCP(task) {
  run('uv', ['sync', '--locked'], 'apps/mcp');
  if (task === 'gen') run('uv', ['run', '--locked', 'python', 'scripts/generate.py'], 'apps/mcp');
  else if (task === 'lint') {
    run('uv', ['lock', '--check'], 'apps/mcp');
    run('uv', ['run', '--locked', 'ruff', 'check', '.'], 'apps/mcp');
    run('uv', ['run', '--locked', 'ruff', 'format', '--check', '.'], 'apps/mcp');
    run('uv', ['run', '--locked', 'pyright'], 'apps/mcp');
    run('uv', ['run', '--locked', 'python', 'scripts/generate.py', '--check'], 'apps/mcp');
  } else if (task === 'test') run('uv', ['run', '--locked', 'pytest'], 'apps/mcp');
  else if (task === 'build') run('uv', ['build'], 'apps/mcp');
  else if (task === 'dev') run('uv', ['run', '--locked', 'opennavo-mcp'], 'apps/mcp');
  else throw new Error(`Unknown MCP task: ${task}`);
}
