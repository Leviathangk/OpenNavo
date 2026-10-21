// Hands-off release (owner decision 2026-10-21). On every CI completion:
// - rerun failed jobs whose logs show a known infrastructure or timing flake;
// - once Backend CI, Frontend CI and Security checks all passed on a main
//   commit that is still the tip of main, dispatch its production deploy, once,
//   if anything that ships changed since the last successful deploy.
// The deploy itself still checks CI (require-ci.mjs) before releasing.
import { readFileSync } from 'node:fs';
import { pathToFileURL } from 'node:url';

export const GATE = ['ci-server.yml', 'ci-frontend.yml', 'ci-security.yml'];
export const FLAKES = /429 Too Many Requests|toomanyrequests|sixteen_thousand_import_and_short_queries/;
export const MAX_ATTEMPTS = 3;

// Paths that end up in the server, web, admin or MCP images or the deployment.
// The dev and E2E stacks under ops/ (and ops tests) never reach production.
export const SHIPS = [
  /^apps\/(server|web|admin|mcp)\//,
  /^packages\//,
  /^ops\/(?!(?:e2e\.py|test_[^/]*\.py|docker-compose\.(?:dev|e2e)(?:\.[^/]+)?\.yml|Dockerfile\.minio)$)/,
  /^(package\.json|pnpm-lock\.yaml|pnpm-workspace\.yaml)$/
];
export const ships = files => files === null || files.some(file => SHIPS.some(pattern => pattern.test(file)));

// The revision of the newest successful deploy: the SHA in its title, or the
// main commit it ran on when it was dispatched for "main".
export function lastDeployed(runs) {
  const run = runs
    .filter(candidate => candidate.conclusion === 'success')
    .sort((a, b) => b.run_number - a.run_number)[0];
  return run ? (run.display_title.match(/\b[0-9a-f]{40}\b/)?.[0] ?? run.head_sha) : null;
}

export const flaky = logs => logs.length > 0 && logs.every(log => FLAKES.test(log));
export const passed = run => run?.status === 'completed' && run.conclusion === 'success';
export const deployed = (runs, sha) =>
  runs.some(run => run.display_title.includes(sha) && (run.status !== 'completed' || run.conclusion === 'success'));

function client(repository, token) {
  return async (path, init = {}) => {
    const response = await fetch(`https://api.github.com/repos/${repository}${path}`, {
      ...init,
      headers: {
        Authorization: `Bearer ${token}`,
        Accept: 'application/vnd.github+json',
        'Content-Type': 'application/json'
      },
      signal: AbortSignal.timeout(60_000)
    });
    if (!response.ok)
      throw new Error(`${init.method ?? 'GET'} ${path}: HTTP ${response.status} ${await response.text()}`);
    if (response.status === 204) return null;
    return (response.headers.get('content-type') ?? '').includes('json') ? response.json() : response.text();
  };
}

async function rerunFlakes(api, run) {
  if (run.run_attempt >= MAX_ATTEMPTS) {
    console.log(`${run.name}: failed after ${run.run_attempt} attempts; not retrying`);
    return;
  }
  const { jobs } = await api(`/actions/runs/${run.id}/jobs?per_page=100`);
  const logs = [];
  for (const job of jobs.filter(candidate => candidate.conclusion === 'failure')) {
    logs.push(await api(`/actions/jobs/${job.id}/logs`).catch(() => ''));
  }
  if (!flaky(logs)) {
    console.log(`${run.name}: the failure is not a known flake`);
    return;
  }
  await api(`/actions/runs/${run.id}/rerun-failed-jobs`, { method: 'POST' });
  console.log(`${run.name}: reran the failed jobs after a known flake`);
}

async function deployMain(api, sha) {
  const { commit } = await api('/branches/main');
  if (commit.sha !== sha) {
    console.log(`${sha} is no longer the tip of main; the newer commit ships itself`);
    return;
  }
  for (const workflow of GATE) {
    const { workflow_runs: runs } = await api(
      `/actions/workflows/${workflow}/runs?head_sha=${sha}&event=push&per_page=20`
    );
    const latest = runs.filter(run => run.head_branch === 'main').sort((a, b) => b.run_number - a.run_number)[0];
    if (!passed(latest)) {
      console.log(`${sha}: waiting for ${workflow}`);
      return;
    }
  }
  const { workflow_runs: deploys } = await api('/actions/workflows/deploy-production.yml/runs?per_page=50');
  if (deployed(deploys, sha)) {
    console.log(`${sha} is already deploying or deployed`);
    return;
  }
  const live = lastDeployed(deploys);
  let files = null;
  if (live) {
    const comparison = await api(`/compare/${live}...${sha}`);
    files =
      comparison.files?.length < 300
        ? comparison.files.flatMap(file => [file.filename, file.previous_filename].filter(Boolean))
        : null;
  }
  if (!ships(files)) {
    console.log(`${sha}: nothing that ships changed since ${live}; not deploying`);
    return;
  }
  await api('/actions/workflows/deploy-production.yml/dispatches', {
    method: 'POST',
    body: JSON.stringify({ ref: 'main', inputs: { revision: sha } })
  });
  console.log(`dispatched the production deploy of ${sha}`);
}

async function main() {
  const { workflow_run: run } = JSON.parse(readFileSync(process.env.GITHUB_EVENT_PATH, 'utf8'));
  const api = client(process.env.GITHUB_REPOSITORY, process.env.GITHUB_TOKEN);
  if (run.conclusion === 'failure') return rerunFlakes(api, run);
  if (run.conclusion === 'success' && run.event === 'push' && run.head_branch === 'main')
    return deployMain(api, run.head_sha);
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) await main();
