import { execFileSync } from 'node:child_process';

const sha = execFileSync('git', ['rev-parse', 'HEAD'], { encoding: 'utf8' }).trim();
execFileSync('git', ['fetch', '--no-tags', 'origin', 'main'], { stdio: 'ignore' });
execFileSync('git', ['merge-base', '--is-ancestor', sha, 'FETCH_HEAD'], { stdio: 'ignore' });
const repository = process.env.GITHUB_REPOSITORY;
const token = process.env.GITHUB_TOKEN;
if (!repository || !token) throw new Error('GitHub authentication is required');
// Production deploys and server, web and MCP releases need these; a desktop
// release (REQUIRE_DESKTOP_CI) also needs the desktop builds.
const workflows = ['ci-server.yml', 'ci-frontend.yml', 'ci-security.yml'];
if (process.env.REQUIRE_DESKTOP_CI === 'true') workflows.push('ci-desktop.yml');
for (const workflow of workflows) {
  const url = `https://api.github.com/repos/${repository}/actions/workflows/${workflow}/runs?head_sha=${sha}&event=push&per_page=100`;
  const response = await fetch(url, {
    headers: { Authorization: `Bearer ${token}`, Accept: 'application/vnd.github+json' },
    signal: AbortSignal.timeout(30_000)
  });
  if (!response.ok) throw new Error(`Cannot check ${workflow}: HTTP ${response.status}`);
  const { workflow_runs: runs } = await response.json();
  const latest = runs.filter(run => run.head_branch === 'main').sort((a, b) => b.run_number - a.run_number)[0];
  if (latest?.status !== 'completed' || latest.conclusion !== 'success') {
    throw new Error(`${workflow} must pass for ${sha} on main before releasing`);
  }
  console.log(`${workflow}: passed for ${sha}`);
}
