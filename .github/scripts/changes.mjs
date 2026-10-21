// Which CI areas a change touches, written to GITHUB_OUTPUT for job conditions.
// Pull requests run fast checks for the areas they touch. Pushes to main add
// full depth (integration tests with -race, full-stack E2E, Lighthouse) for the
// areas they touch; scheduled and dispatched runs cover every area at full
// depth, and a pull request labelled full-ci runs full depth too. Jobs are
// skipped with `if`, never with workflow path filters, so every workflow still
// reports a conclusion that the deploy gate (require-ci.mjs) can read.
import { appendFileSync, readFileSync } from 'node:fs';
import { pathToFileURL } from 'node:url';

const workspace = [
  /^(Makefile|package\.json|pnpm-lock\.yaml|pnpm-workspace\.yaml)$/,
  /^tooling\/scripts\/(workspace|tasks\/shared)\.mjs$/
];
export const AREAS = {
  server: [
    /^apps\/server\//,
    /^packages\/shared\/src\/locales\.json$/,
    /^tooling\/i18n\//,
    /^tooling\/scripts\/tasks\/server\.mjs$/,
    /^\.github\/workflows\/ci-server\.yml$/,
    ...workspace
  ],
  mcp: [
    /^apps\/mcp\//,
    /^apps\/server\/api\/admin\.openapi\.yaml$/,
    /^apps\/server\/internal\/http\/admin\/(agent\.go|hermes_test\.go)$/,
    /^packages\/shared\/src\/locales\.json$/,
    /^tooling\/scripts\/tasks\/server\.mjs$/,
    /^\.github\/workflows\/(ci-mcp|ci-server|release-mcp)\.yml$/
  ],
  frontend: [
    /^apps\/(web|admin|desktop)\//,
    /^packages\//,
    /^tooling\/(config|i18n|scripts)\//,
    /^docs\/design\/logo\/source\//,
    /^\.github\/workflows\/ci-frontend\.yml$/,
    ...workspace
  ],
  // Rust, Swift and Tauri builds. Shared packages reach the desktop only through
  // its web view, which the frontend jobs typecheck, test and build.
  desktop: [/^apps\/desktop\//, /^tooling\/scripts\/check-notices\.py$/, /^\.github\/workflows\/ci-desktop\.yml$/],
  // Full-stack web and admin E2E run against the seeded API.
  e2e: [
    /^apps\/(server|web|admin)\//,
    /^packages\//,
    /^ops\/(e2e\.py|docker-compose\.e2e\.yml|Dockerfile\.minio)$/,
    /^\.github\/actions\/e2e-backend\//,
    /^\.github\/workflows\/ci-frontend\.yml$/,
    ...workspace
  ]
};
const everything = Object.fromEntries(Object.keys(AREAS).map(area => [area, true]));
const GATE = ['ci-server.yml', 'ci-frontend.yml', 'ci-security.yml'];
// Desktop CI is outside the deploy gate, so its main pushes compare with its
// own last green commit; the gate workflows ignore desktop results.
export const baseline = workflowRef =>
  /\/\.github\/workflows\/ci-desktop\.yml@/.test(workflowRef ?? '') ? ['ci-desktop.yml'] : GATE;

// Main commits on which every gate workflow passed, newest first (the order of
// the first workflow's runs).
export function greenCommits(runsByWorkflow) {
  const [first, ...rest] = runsByWorkflow.map(runs =>
    runs
      .filter(run => run.head_branch === 'main' && run.status === 'completed' && run.conclusion === 'success')
      .map(run => run.head_sha)
  );
  return [...new Set(first)].filter(sha => rest.every(list => list.includes(sha)));
}

export function classify(files) {
  return Object.fromEntries(
    Object.entries(AREAS).map(([area, patterns]) => [
      area,
      files.some(file => patterns.some(pattern => pattern.test(file)))
    ])
  );
}

// Areas and depth for a GitHub event; `files` lists the changed paths, or is
// null when they cannot be listed completely (then every area runs).
export function plan(eventName, payload, files) {
  if (eventName === 'schedule' || eventName === 'workflow_dispatch') return { ...everything, full: true };
  const areas = files ? classify(files) : everything;
  if (eventName === 'push') return { ...areas, full: true };
  const full = (payload.pull_request?.labels ?? []).some(label => label.name === 'full-ci');
  return { ...areas, full };
}

async function get(path) {
  const response = await fetch(`https://api.github.com/repos/${process.env.GITHUB_REPOSITORY}${path}`, {
    headers: { Authorization: `Bearer ${process.env.GITHUB_TOKEN}`, Accept: 'application/vnd.github+json' },
    signal: AbortSignal.timeout(30_000)
  });
  if (!response.ok) throw new Error(`GET ${path}: HTTP ${response.status}`);
  return response.json();
}

async function changedFiles(eventName, payload) {
  if (eventName === 'pull_request') {
    const files = [];
    for (let page = 1; page <= 30; page++) {
      const batch = await get(`/pulls/${payload.pull_request.number}/files?per_page=100&page=${page}`);
      files.push(
        ...batch.map(file => file.filename),
        ...batch.flatMap(file => (file.previous_filename ? [file.previous_filename] : []))
      );
      if (batch.length < 100) return files;
    }
    return null;
  }
  if (eventName === 'push') {
    // Compare with the newest main commit on which every gate workflow passed,
    // not only the previous push: areas an earlier push left red or unchecked
    // are checked again, so a green tip means everything since was tested.
    const runs = await Promise.all(
      baseline(process.env.GITHUB_WORKFLOW_REF).map(workflow =>
        get(`/actions/workflows/${workflow}/runs?branch=main&event=push&status=success&per_page=50`).then(
          result => result.workflow_runs
        )
      )
    );
    for (const base of greenCommits(runs)) {
      if (base === payload.after) continue;
      const comparison = await get(`/compare/${base}...${payload.after}`);
      if (comparison.status !== 'ahead') continue;
      const files = comparison.files ?? [];
      if (files.length >= 300) return null;
      console.log(`comparing with ${base}, the newest main commit with green CI`);
      return [
        ...files.map(file => file.filename),
        ...files.flatMap(file => (file.previous_filename ? [file.previous_filename] : []))
      ];
    }
    return null;
  }
  return null;
}

async function main() {
  const eventName = process.env.GITHUB_EVENT_NAME;
  const payload = JSON.parse(readFileSync(process.env.GITHUB_EVENT_PATH, 'utf8'));
  // The event keeps the labels it fired with: `gh pr create --label` adds them
  // just after opening, and re-runs reuse the event. Read the current ones, so
  // labelling full-ci and re-running the workflow takes effect.
  if (eventName === 'pull_request') payload.pull_request.labels = (await get(`/pulls/${payload.pull_request.number}`)).labels;
  const files = eventName === 'pull_request' || eventName === 'push' ? await changedFiles(eventName, payload) : null;
  const result = plan(eventName, payload, files);
  console.log(files ? `${files.length} changed paths` : 'every area');
  for (const [key, value] of Object.entries(result)) {
    console.log(`${key}: ${value}`);
    appendFileSync(process.env.GITHUB_OUTPUT, `${key}=${value}\n`);
  }
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) await main();
