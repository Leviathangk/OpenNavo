// Link apps (store/security plan §4), pure for testing. OpenNavo hosts nothing: Download points at the
// developer's own download page or the project's GitHub release, in the same order as the desktop app.
import type { PublicComponents } from '@opennavo/api';

export type LinkInfo = PublicComponents['schemas']['LinkInfo'];
export type ReleaseAsset = PublicComponents['schemas']['ReleaseAsset'];

/** Link addresses come from admin input; only https ever becomes an href. */
export function httpsUrl(url: string | null | undefined): string | undefined {
  if (!url) return undefined;
  try {
    return new URL(url).protocol === 'https:' ? url : undefined;
  } catch {
    return undefined;
  }
}

/** The latest GitHub release page, else the project's newest release. */
export function githubReleaseUrl(link: Pick<LinkInfo, 'github'>): string | undefined {
  if (!link.github) return undefined;
  return httpsUrl(link.github.latest?.url) ?? `https://github.com/${link.github.repo}/releases/latest`;
}

/** Where Download goes: the official download page, then GitHub, then the homepage. */
export function linkDownloadUrl(link: Pick<LinkInfo, 'downloadUrl' | 'github' | 'homepage'>): string | undefined {
  return httpsUrl(link.downloadUrl) ?? githubReleaseUrl(link) ?? httpsUrl(link.homepage);
}

const host = (url: string) => new URL(url).hostname.toLowerCase().replace(/^www\./, '');

/** Where the files come from, for "Download from …": lumen.app or github.com/owner/name. */
export function linkSource(link: Pick<LinkInfo, 'downloadUrl' | 'github' | 'homepage'>): string | undefined {
  const download = httpsUrl(link.downloadUrl);
  if (download) return host(download);
  if (link.github) return `github.com/${link.github.repo}`;
  const homepage = httpsUrl(link.homepage);
  return homepage ? host(homepage) : undefined;
}

/**
 * downloadUrl for structured data: only an address on the developer's own site (the homepage's
 * domain) or in the app's GitHub repository, so a mirror is never presented as the official file.
 */
export function officialDownloadUrl(link: Pick<LinkInfo, 'downloadUrl' | 'github' | 'homepage'>): string | undefined {
  const download = httpsUrl(link.downloadUrl);
  const homepage = httpsUrl(link.homepage);
  if (download && homepage) {
    const [file, site] = [host(download), host(homepage)];
    // A GitHub homepage says nothing about who owns other github.com paths; the repository rule covers those.
    if (site !== 'github.com' && (file === site || file.endsWith(`.${site}`))) return download;
  }
  if (download && link.github) {
    const url = new URL(download);
    if (url.hostname === 'github.com' && url.pathname.toLowerCase().startsWith(`/${link.github.repo.toLowerCase()}/`))
      return download;
  }
  return githubReleaseUrl(link);
}

/** Notes in the page language, else English, else whichever language has text. */
export function linkNotes(notes: LinkInfo['notes'], locale: string): string {
  if (!notes) return '';
  const texts = notes as Record<string, string | null | undefined>;
  return texts[locale] || texts['en-US'] || Object.values(texts).find(Boolean) || '';
}
