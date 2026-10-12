// Desktop release artifact names, checksums, draft registration payloads, and cask rendering (06 §15, 03 §13.3).
// release-desktop.yml calls these through release-artifacts.mjs / register-release.mjs / render-cask.mjs; unit tests import the pure functions directly.
import { RELEASE_NOTE_LOCALES, validateNotes } from './release-notes.mjs';
import { createHash } from 'node:crypto';
import { readFileSync, statSync } from 'node:fs';

/** Three build targets: updater packages for both architectures and a Universal DMG for initial installation (registration target must be one of these). */
export const TARGETS = {
  'darwin-aarch64': {
    rustTarget: 'aarch64-apple-darwin',
    bundle: 'app',
    file: () => 'OpenNavo_aarch64.app.tar.gz'
  },
  'darwin-x86_64': {
    rustTarget: 'x86_64-apple-darwin',
    bundle: 'app',
    file: () => 'OpenNavo_x64.app.tar.gz'
  },
  'dmg-universal': {
    rustTarget: 'universal-apple-darwin',
    bundle: 'dmg',
    file: version => `OpenNavo_${version}_universal.dmg`
  }
};

const VERSION = /^\d+\.\d+\.\d+(-[0-9A-Za-z.]+)?$/;

/** Same format as the DesktopReleaseCreate.version contract. */
export function isReleaseVersion(version) {
  return VERSION.test(version);
}

/** Versions with prerelease suffixes (e.g. 0.3.0-beta.1) use beta; all others use stable. */
export function channelOf(version) {
  return version.includes('-') ? 'beta' : 'stable';
}

/** Tag desktop-v0.3.0 → 0.3.0. */
export function versionFromTag(tag) {
  if (!tag.startsWith('desktop-v')) throw new Error(`invalid desktop release tag: ${tag}`);
  const version = tag.slice('desktop-v'.length);
  if (!isReleaseVersion(version)) throw new Error(`invalid desktop release tag: ${tag}`);
  return version;
}

export function artifactUrl(baseUrl, version, file) {
  return `${baseUrl.replace(/\/+$/, '')}/${version}/${file}`;
}

export function githubBase(repository) {
  if (!/^[A-Za-z0-9][A-Za-z0-9-]*\/[A-Za-z0-9_.-]+$/.test(repository) || repository.endsWith('/..')) {
    throw new Error('invalid GitHub repository');
  }
  return `https://github.com/${repository}/releases/download`;
}

export function githubArtifactUrl(repository, version, file) {
  if (!isReleaseVersion(version)) throw new Error('invalid version');
  if (!/^[A-Za-z0-9_.-]+$/.test(file) || file === '..') throw new Error('invalid asset name');
  return `${githubBase(repository)}/desktop-v${version}/${file}`;
}

export function describeFile(path) {
  return {
    bytes: statSync(path).size,
    sha256: createHash('sha256').update(readFileSync(path)).digest('hex')
  };
}

/** Draft registration payload (DesktopReleaseCreate contract); DMGs have no update signature. */
export function releaseBody({ version, channel, artifacts, notesEn, notes, minMacos = '13.0' }) {
  if (!isReleaseVersion(version)) throw new Error(`invalid version: ${version}`);
  const missing = Object.keys(TARGETS).filter(target => !artifacts.some(item => item.target === target));
  if (missing.length) throw new Error(`missing artifacts: ${missing.join(', ')}`);
  if (channel !== channelOf(version)) throw new Error('version/channel mismatch');
  if (artifacts.length !== Object.keys(TARGETS).length) throw new Error('duplicate or unknown artifacts');
  return {
    version,
    channel,
    minMacos,
    sourceLocale: 'en-US',
    i18n: Object.fromEntries(
      (notes ? RELEASE_NOTE_LOCALES : ['en-US']).map(locale => [
        locale,
        { notes: validateNotes(notes ? notes[locale] : notesEn, version, locale) }
      ])
    ),
    artifacts: artifacts.map(({ target, url, signature, bytes, sha256 }) => ({
      target,
      url,
      signature: target === 'dmg-universal' ? null : (signature ?? null),
      bytes,
      sha256
    }))
  };
}

/** Render the cask template; validate all values so arbitrary text cannot enter Ruby source. */
export function renderCask(template, { version, sha256, downloadUrl, manifestUrl, homepage }) {
  if (!isReleaseVersion(version)) throw new Error(`invalid version: ${version}`);
  if (!/^[0-9a-f]{64}$/.test(sha256)) throw new Error('invalid sha256');
  for (const url of [downloadUrl, manifestUrl, homepage]) {
    if (!/^https:\/\/[A-Za-z0-9.-]+(\/[A-Za-z0-9._/-]*)?$/.test(url)) throw new Error(`invalid url: ${url}`);
  }
  // Omit the template's introductory comment from the tap.
  const body = template.slice(template.indexOf('cask "'));
  return body
    .replaceAll('{{version}}', version)
    .replaceAll('{{sha256}}', sha256)
    .replaceAll('{{downloadUrl}}', downloadUrl)
    .replaceAll('{{manifestUrl}}', manifestUrl)
    .replaceAll('{{homepage}}', homepage);
}
