/** The source fields used by both full desktop details and the website's slimmed SSR payload. */
interface SourcePackage {
  kind: string;
  version?: string;
  sourceUrl?: string | null;
  downloadUrl?: string | null;
  downloadSha256?: string | null;
  facts?: {
    overview: { version: string; downloadUrl?: string | null; sha256?: string | null };
    requirements: readonly {
      architectures: readonly string[];
      versions: readonly string[];
      downloadUrl?: string | null;
      sha256?: string | null;
      evidence: { sourceUrl: string; status: string; checkedAt?: string | null };
    }[];
  } | null;
}

const https = (value: string | null | undefined) => {
  try {
    return value && new URL(value).protocol === 'https:' ? value : undefined;
  } catch {
    return undefined;
  }
};
const checksum = (value: string | null | undefined): value is string => Boolean(value && /^[a-f0-9]{64}$/i.test(value));

/** Homebrew source evidence remains inspectable when a package is disabled, deprecated or paused. */
export function toPackageSource(pkg: SourcePackage) {
  if (pkg.kind !== 'cask') return null;
  const overview = pkg.facts?.overview;
  const version = overview?.version || pkg.version;
  const rows = (pkg.facts?.requirements ?? []).filter(
    row =>
      row.architectures.length > 0 &&
      Boolean(pkg.sourceUrl) &&
      row.evidence.sourceUrl === pkg.sourceUrl &&
      row.versions.some(value => value === version)
  );
  const dated = rows.filter(
    row =>
      ['verified', 'stale'].includes(row.evidence.status) &&
      Boolean(row.evidence.checkedAt) &&
      Number.isFinite(Date.parse(row.evidence.checkedAt!))
  );
  const urls = [...new Set(rows.flatMap(row => https(row.downloadUrl) ?? []))];
  if (!urls.length) {
    const fallback = https(overview?.downloadUrl ?? pkg.downloadUrl);
    if (fallback) urls.push(fallback);
  }
  if (!urls.length) return null;
  const entries = urls.map(url => {
    const matching = rows.filter(row => row.downloadUrl === url);
    const hashes = matching.map(row => row.sha256).filter(checksum);
    const unique = new Set(hashes.map(sha => sha.toLowerCase()));
    const fallback = overview?.sha256 ?? pkg.downloadSha256;
    return {
      host: new URL(url).hostname,
      sha256:
        matching.length > 0 && hashes.length === matching.length && unique.size === 1
          ? hashes[0]!
          : matching.length === 0 && checksum(fallback)
            ? fallback
            : null
    };
  });
  const checkedAt =
    dated.length === rows.length
      ? dated.map(row => row.evidence.checkedAt!).sort((a, b) => Date.parse(a) - Date.parse(b))[0]
      : undefined;
  return {
    entries,
    checkedAt,
    verified: Boolean(
      checkedAt &&
      rows.length &&
      rows.every(row => row.evidence.status === 'verified' && https(row.downloadUrl)) &&
      entries.every(entry => entry.sha256)
    )
  };
}

export type PackageSource = NonNullable<ReturnType<typeof toPackageSource>>;
