// @vitest-environment node
import { createHash } from 'node:crypto';
import { mkdtempSync, writeFileSync, readFileSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { describe, expect, it, vi } from 'vitest';
import { prepareAssets, publishRelease, verifyDownload, githubClient } from '../scripts/github-release.mjs';
import { publishedCask } from '../scripts/published-cask.mjs';
import { TARGETS, githubArtifactUrl, describeFile } from '../scripts/release-lib.mjs';

const repository = 'acme/opennavo';
const version = '0.3.0';
const notesEn = '## Improvements\n\n- Improved collection navigation.';
const sha256 = createHash('sha256').update('content').digest('hex');
const assets = [{ name: 'app.tar.gz', bytes: 7, sha256, path: '/unused' }];
function fixture({ draft = true, remote = [], prerelease = false, exists = true } = {}) {
  const release = { id: 1, draft, tag_name: `desktop-v${version}`, prerelease };
  const client = {
    repository: vi.fn(async () => ({ private: false })),
    tag: vi.fn(async () => ({})),
    get: vi.fn(async () => (exists ? release : null)),
    create: vi.fn(async () => release),
    assets: vi.fn(async () => remote),
    upload: vi.fn(async (_, a) => ({
      name: a.name,
      size: a.bytes,
      digest: `sha256:${a.sha256}`,
      state: 'uploaded'
    })),
    updateNotes: vi.fn(async () => ({})),
    publish: vi.fn(async () => ({})),
    verify: vi.fn(async () => {})
  };
  return client;
}
const good = { name: 'app.tar.gz', size: 7, digest: `sha256:${sha256}`, state: 'uploaded' };
const run = client => publishRelease({ repository, version, assets, client, notesEn });

describe('GitHub releases and failure recovery', () => {
  it.each([undefined, '', '## Improvements\n\n- 改进导航。'])(
    'rejects missing or Chinese notes before remote changes',
    async invalidNotes => {
      const client = fixture();
      await expect(publishRelease({ repository, version, assets, client, notesEn: invalidNotes })).rejects.toThrow();
      expect(client.repository).not.toHaveBeenCalled();
      expect(client.updateNotes).not.toHaveBeenCalled();
    }
  );
  it('Uploads the complete draft before publishing, then verifies anonymous downloads', async () => {
    const client = fixture({ exists: false });
    await run(client);
    expect(client.create).toHaveBeenCalledWith('desktop-v0.3.0', false);
    expect(client.publish).toHaveBeenCalledWith(1, false);
    expect(client.updateNotes).toHaveBeenCalledWith(1, `# OpenNavo ${version}\n\n${notesEn}\n`);
    expect(client.publish.mock.invocationCallOrder[0]).toBeGreaterThan(client.updateNotes.mock.invocationCallOrder[0]);
    expect(client.publish.mock.invocationCallOrder[0]).toBeGreaterThan(client.upload.mock.invocationCallOrder[0]);
    expect(client.verify.mock.invocationCallOrder[0]).toBeGreaterThan(client.publish.mock.invocationCallOrder[0]);
    expect(client.verify).toHaveBeenCalledWith(githubArtifactUrl(repository, version, 'app.tar.gz'), assets[0]);
  });
  it('Only verifies identical published assets without uploading or publishing again', async () => {
    const client = fixture({ draft: false, remote: [good] });
    await run(client);
    expect(client.upload).not.toHaveBeenCalled();
    expect(client.publish).not.toHaveBeenCalled();
    expect(client.verify).toHaveBeenCalledOnce();
    expect(client.updateNotes).toHaveBeenCalledWith(1, `# OpenNavo ${version}\n\n${notesEn}\n`);
  });
  it.each([
    { draft: false, remote: [] },
    { draft: true, remote: [{ ...good, digest: 'sha256:wrong' }] },
    { draft: true, remote: [{ ...good, state: 'starter' }] },
    { draft: true, remote: [{ ...good, name: 'unexpected' }] },
    { draft: true, remote: [good, good] },
    { draft: true, remote: [good], prerelease: true }
  ])('Rejects missing or conflicting assets and channels without overwriting: %j', async options => {
    const client = fixture(options);
    await expect(run(client)).rejects.toThrow();
    expect(client.publish).not.toHaveBeenCalled();
    expect(client.upload).not.toHaveBeenCalled();
  });
  it('Keeps drafts after upload failures and does not create releases for private repositories or missing tags', async () => {
    const client = fixture();
    client.upload.mockRejectedValue(new Error('network'));
    await expect(run(client)).rejects.toThrow('network');
    expect(client.publish).not.toHaveBeenCalled();
    client.repository.mockResolvedValue({ private: true });
    await expect(run(client)).rejects.toThrow('public');
    expect(client.create).not.toHaveBeenCalled();
  });
  it('Creates beta prereleases without replacing latest', async () => {
    const client = fixture({ exists: false });
    await publishRelease({ repository, version: '0.3.0-beta.1', assets, client, notesEn });
    expect(client.create).toHaveBeenCalledWith('desktop-v0.3.0-beta.1', true);
    expect(client.publish).toHaveBeenCalledWith(1, true);
  });
  it('Verifies downloads without tokens and rejects size or hash mismatches', async () => {
    const fetcher = vi.fn(async () => new Response('content'));
    await verifyDownload('https://github.com/asset', assets[0], fetcher);
    expect(fetcher.mock.calls[0][1]).not.toHaveProperty('headers');
    await expect(
      verifyDownload('https://github.com/asset', { ...assets[0], sha256: 'wrong' }, fetcher)
    ).rejects.toThrow('checksum');
    await expect(verifyDownload('https://github.com/asset', { ...assets[0], bytes: 2 }, fetcher)).rejects.toThrow(
      'size'
    );
    await expect(
      verifyDownload('https://github.com/asset', assets[0], async () => new Response('', { status: 404 }))
    ).rejects.toThrow('404');
  });
  it('Disallows authenticated API redirects and marks only stable publications as latest', async () => {
    const fetcher = vi.fn(async () => Response.json({ id: 1 }));
    const client = githubClient(repository, 'fixture-only', fetcher);
    await client.create('desktop-v0.3.0', false);
    await client.publish(1, false);
    await client.publish(2, true);
    expect(fetcher.mock.calls.map(([, options]) => JSON.parse(options.body).make_latest)).toEqual([
      'false',
      'true',
      'false'
    ]);
    for (const [, options] of fetcher.mock.calls) {
      expect(options.redirect).toBe('error');
    }
  });
});

it('Preflights artifacts for all three targets, signatures, hashes, and fixed-version URLs', () => {
  const dir = mkdtempSync(join(tmpdir(), 'opennavo-github-release-'));
  try {
    for (const [target, spec] of Object.entries(TARGETS)) {
      const file = spec.file(version);
      writeFileSync(join(dir, file), 'content');
      const signature = spec.bundle === 'app' ? 'fixture-signature' : null;
      if (signature) writeFileSync(join(dir, `${file}.sig`), signature);
      writeFileSync(
        join(dir, `${target}.json`),
        JSON.stringify({
          target,
          file,
          signature,
          url: githubArtifactUrl(repository, version, file),
          ...describeFile(join(dir, file))
        })
      );
    }
    expect(prepareAssets(dir, repository, version)).toHaveLength(6);
    expect(readFileSync(join(dir, 'SHA256SUMS'), 'utf8')).toContain('OpenNavo_x64.app.tar.gz.sig');
    writeFileSync(join(dir, 'OpenNavo_x64.app.tar.gz.sig'), 'changed');
    expect(() => prepareAssets(dir, repository, version)).toThrow('signature');
    writeFileSync(join(dir, 'OpenNavo_x64.app.tar.gz'), 'tampered');
    expect(() => prepareAssets(dir, repository, version)).toThrow('artifact');
  } finally {
    rmSync(dir, { recursive: true, force: true });
  }
});

describe('Tap publication gate', () => {
  const values = {
    version,
    repository,
    'api-base': 'https://api.example.test/api/v1',
    'manifest-url': 'https://cdn.example.test/desktop/stable/latest.json',
    homepage: 'https://example.test/'
  };
  const dmg = {
    target: 'dmg-universal',
    url: githubArtifactUrl(repository, version, `OpenNavo_${version}_universal.dmg`),
    bytes: 7,
    sha256
  };
  const fetcher = (published = version, manifestVersion = version, artifact = dmg) =>
    vi.fn(async url => {
      if (url.includes('/desktop/releases/'))
        return Response.json({
          code: '0000',
          data: { version: published, channel: 'stable', downloads: [artifact] }
        });
      if (url.endsWith('latest.json')) return Response.json({ version: manifestVersion });
      return new Response('content');
    });
  it('Renders the GitHub cask when the stable release, manifest, and anonymous DMG contents agree', async () => {
    const cask = await publishedCask(values, fetcher());
    expect(cask).toContain(dmg.url);
    expect(cask).toContain(values['manifest-url']);
  });
  it('Rejects unpublished backend releases, unchanged manifests, and mismatched DMG URLs or contents', async () => {
    await expect(publishedCask(values, fetcher('0.2.0'))).rejects.toThrow('published stable');
    await expect(publishedCask(values, fetcher(version, '0.2.0'))).rejects.toThrow('manifest');
    await expect(
      publishedCask(values, fetcher(version, version, { ...dmg, url: 'https://cdn.example.test/app.dmg' }))
    ).rejects.toThrow('DMG');
    await expect(publishedCask(values, fetcher(version, version, { ...dmg, sha256: 'a'.repeat(64) }))).rejects.toThrow(
      'checksum'
    );
    await expect(publishedCask({ ...values, version: '0.3.0-beta.1' }, fetcher())).rejects.toThrow('stable version');
  });
});
