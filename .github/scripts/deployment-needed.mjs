// Apply the automatic ship filter to manual dispatches too, before rebuilding web.
// Compare full local Git trees, so a large desktop-only diff is not truncated by the API.
import { execFileSync } from 'node:child_process';
import { appendFileSync } from 'node:fs';
import { pathToFileURL } from 'node:url';
import { lastDeployed, ships } from './ship.mjs';

export function deploymentNeeded(runs, sha, changedFiles) {
  const base = lastDeployed(runs.filter(run => run.status === 'completed' && run.conclusion === 'success'));
  if (!base) return true;
  if (base === sha) return false;
  return ships(changedFiles(base, sha));
}

async function main() {
  const sha = execFileSync('git', ['rev-parse', 'HEAD'], { encoding: 'utf8' }).trim();
  const response = await fetch(
    `https://api.github.com/repos/${process.env.GITHUB_REPOSITORY}/actions/workflows/deploy-production.yml/runs?status=success&per_page=100`,
    {
      headers: {
        Authorization: `Bearer ${process.env.GITHUB_TOKEN}`,
        Accept: 'application/vnd.github+json'
      },
      signal: AbortSignal.timeout(30_000)
    }
  );
  if (!response.ok) throw new Error(`Cannot check deployed revision: HTTP ${response.status}`);
  const { workflow_runs: runs } = await response.json();
  const needed = deploymentNeeded(runs, sha, (base, head) =>
    execFileSync('git', ['diff', '--name-only', '--no-renames', '-z', base, head], {
      encoding: 'utf8'
    })
      .split('\0')
      .filter(Boolean)
  );
  appendFileSync(process.env.GITHUB_OUTPUT, `needed=${needed}\n`);
  console.log(
    needed ? 'Production inputs changed; deploying.' : 'Production inputs unchanged; keeping web and its caches.'
  );
}
if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) await main();
