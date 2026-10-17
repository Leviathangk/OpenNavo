// GitHub is the only package download source: validate all assets, publish them, then register the backend draft.
import { createHash } from 'node:crypto';
import { openAsBlob, readFileSync, writeFileSync } from 'node:fs';
import { join, resolve } from 'node:path';
import { pathToFileURL } from 'node:url';
import { parseArgs } from 'node:util';
import { TARGETS, channelOf, describeFile, githubArtifactUrl, githubBase, isReleaseVersion } from './release-lib.mjs';
import { loadReleaseNotes, validateNotes } from './release-notes.mjs';

export function prepareAssets(dir, repository, version) {
  githubBase(repository);
  if (!isReleaseVersion(version)) throw new Error('invalid version');
  const artifacts = Object.entries(TARGETS).map(([target, spec]) => {
    const row = JSON.parse(readFileSync(join(dir, `${target}.json`), 'utf8'));
    const file = spec.file(version);
    const actual = describeFile(join(dir, file));
    if (
      actual.bytes <= 0 ||
      row.target !== target ||
      row.file !== file ||
      row.bytes !== actual.bytes ||
      row.sha256 !== actual.sha256 ||
      row.url !== githubArtifactUrl(repository, version, file)
    )
      throw new Error(`invalid artifact: ${target}`);
    if (target !== 'dmg-universal') {
      const signature = readFileSync(join(dir, `${file}.sig`), 'utf8').trim();
      if (!signature || signature !== row.signature) throw new Error(`invalid signature: ${target}`);
    }
    return row;
  });
  const names = artifacts.flatMap(row => (row.target === 'dmg-universal' ? [row.file] : [row.file, `${row.file}.sig`]));
  // GPL standalone engine corresponding source must be published with every binary release.
  names.push('clamav-1.5.4-corresponding-source.tar.gz');
  const assets = names.map(name => ({
    name,
    path: join(dir, name),
    ...describeFile(join(dir, name))
  }));
  const checksums = assets.map(a => `${a.sha256}  ${a.name}\n`).join('');
  const path = join(dir, 'SHA256SUMS');
  writeFileSync(path, checksums);
  return [...assets, { name: 'SHA256SUMS', path, ...describeFile(path) }];
}

export async function verifyDownload(url, asset, fetcher = fetch) {
  // Public downloads send no GitHub/backend tokens; follow GitHub asset redirects and verify each chunk.
  const response = await fetcher(url, { signal: AbortSignal.timeout(300_000) });
  if (!response.ok || !response.body) throw new Error(`asset download failed: ${asset.name} HTTP ${response.status}`);
  const hash = createHash('sha256');
  let bytes = 0;
  for await (const chunk of response.body) {
    bytes += chunk.length;
    if (bytes > asset.bytes) throw new Error(`asset size mismatch: ${asset.name}`);
    hash.update(chunk);
  }
  if (bytes !== asset.bytes || hash.digest('hex') !== asset.sha256)
    throw new Error(`asset checksum mismatch: ${asset.name}`);
}

export function githubClient(repository, token, fetcher = fetch) {
  githubBase(repository);
  if (!token) throw new Error('GH_TOKEN is required');
  const api = `https://api.github.com/repos/${repository}`;
  async function request(url, options = {}, allowMissing = false) {
    const response = await fetcher(url, {
      ...options,
      redirect: 'error',
      signal: AbortSignal.timeout(300_000),
      headers: {
        Authorization: `Bearer ${token}`,
        Accept: 'application/vnd.github+json',
        'X-GitHub-Api-Version': '2022-11-28',
        ...options.headers
      }
    });
    if (allowMissing && response.status === 404) return null;
    // Do not print response bodies: third-party errors may echo credentials.
    if (!response.ok) throw new Error(`GitHub request failed: HTTP ${response.status}`);
    return response.json();
  }
  const json = (method, body) => ({
    method,
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(body)
  });
  return {
    repository: () => request(api),
    get: tag => request(`${api}/releases/tags/${tag}`, {}, true),
    create: (tag, prerelease) =>
      request(
        `${api}/releases`,
        json('POST', {
          tag_name: tag,
          name: `OpenNavo ${tag.slice('desktop-v'.length)}`,
          draft: true,
          prerelease,
          make_latest: 'false'
        })
      ),
    tag: tag => request(`${api}/git/ref/tags/${tag}`),
    assets: async id => {
      const rows = [];
      for (let page = 1; ; page++) {
        const batch = await request(`${api}/releases/${id}/assets?per_page=100&page=${page}`);
        rows.push(...batch);
        if (batch.length < 100) return rows;
      }
    },
    upload: async (id, asset) =>
      request(
        `https://uploads.github.com/repos/${repository}/releases/${id}/assets?name=${encodeURIComponent(asset.name)}`,
        {
          method: 'POST',
          headers: { 'Content-Type': 'application/octet-stream' },
          body: await openAsBlob(asset.path)
        }
      ),
    publish: (id, prerelease, tag) =>
      request(
        `${api}/releases/${id}`,
        json('PATCH', { tag_name: tag, draft: false, make_latest: prerelease ? 'false' : 'true' })
      ),
    updateNotes: (id, body) => request(`${api}/releases/${id}`, json('PATCH', { body })),
    verify: (url, asset) => verifyDownload(url, asset, fetcher)
  };
}

export async function publishRelease({ repository, version, assets, client, notesEn }) {
  const body = `# OpenNavo ${version}\n\n${validateNotes(notesEn, version, 'en-US')}\n`;
  const tag = `desktop-v${version}`;
  const prerelease = channelOf(version) === 'beta';
  if ((await client.repository()).private) throw new Error('release repository must be public');
  await client.tag(tag); // Do not let GitHub implicitly create a tag pointing to the default branch.
  let release = await client.get(tag);
  if (release && (release.tag_name !== tag || release.prerelease !== prerelease))
    throw new Error('release tag/channel mismatch');
  if (!release) release = await client.create(tag, prerelease);
  const remote = await client.assets(release.id);
  const expected = new Set(assets.map(a => a.name));
  if (remote.some(a => !expected.has(a.name)) || new Set(remote.map(a => a.name)).size !== remote.length) {
    throw new Error('unexpected or duplicate remote assets');
  }
  for (const asset of assets) {
    let found = remote.find(a => a.name === asset.name);
    if (!found) {
      if (!release.draft) throw new Error(`published release missing asset: ${asset.name}`);
      found = await client.upload(release.id, asset);
    }
    // GitHub provides the uploaded content's SHA-256; conservatively reject overwriting older assets without a digest.
    if (found.size !== asset.bytes || found.digest !== `sha256:${asset.sha256}` || found.state !== 'uploaded') {
      throw new Error(`remote asset mismatch: ${asset.name}`);
    }
  }
  await client.updateNotes(release.id, body);
  if (release.draft) await client.publish(release.id, prerelease, tag);
  for (const asset of assets) await client.verify(githubArtifactUrl(repository, version, asset.name), asset);
}

if (process.argv[1] && import.meta.url === pathToFileURL(resolve(process.argv[1])).href) {
  const { values } = parseArgs({
    options: {
      dir: { type: 'string', default: 'release-artifacts' },
      'notes-dir': { type: 'string', default: 'apps/desktop/release-notes' },
      version: { type: 'string' },
      repository: { type: 'string', default: process.env.GITHUB_REPOSITORY }
    }
  });
  const repository = values.repository ?? '';
  const version = values.version ?? '';
  const notes = loadReleaseNotes(values['notes-dir'], version);
  // Fail before any remote writes if registration credentials are missing.
  if (!process.env.ADMIN_API_BASE || !process.env.CI_RELEASE_TOKEN)
    throw new Error('registration configuration is required');
  const assets = prepareAssets(resolve(values.dir), repository, version);
  await publishRelease({
    repository,
    version,
    notesEn: notes['en-US'],
    assets,
    client: githubClient(repository, process.env.GH_TOKEN)
  });
  console.log(`GitHub Release desktop-v${version}: all anonymous downloads verified`);
}
