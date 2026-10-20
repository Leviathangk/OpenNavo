// Which of a link app's release files suit a Mac (store/security plan §4). OpenNavo hosts nothing: this only decides what
// the download panel lists, from the file names the developer chose. Pure, shared by the website and the desktop app.

/** The chips a Mac has. */
export type MacChip = 'arm64' | 'x86_64';

/** What the choice needs of a release file; the catalog reads the chip and the format from the file name. */
export interface LinkAssetInfo {
  name: string;
  arch: MacChip | 'universal' | null;
  format: string;
}

const OTHER_SYSTEM = /(^|[^a-z0-9])(linux|windows|win32|win64|win|ubuntu|debian|android)(?=[^a-z0-9]|$)/i;
const MAC_NAME = /(^|[^a-z0-9])(mac|macos|macosx|osx|darwin)(?=[^a-z0-9]|$)/i;

/**
 * Positively a macOS installer: a disk image or a package, or a ZIP whose name says it is for the Mac. A file named for
 * another system is not, and neither is a ZIP that only names a chip, which says nothing about the system.
 */
export function isMacInstaller(asset: Pick<LinkAssetInfo, 'name' | 'format'>): boolean {
  if (OTHER_SYSTEM.test(asset.name)) return false;
  return asset.format === 'dmg' || asset.format === 'pkg' || (asset.format === 'zip' && MAC_NAME.test(asset.name));
}

/** Whether the file runs on the chip. A file that names no chip is not ruled out; with no chip known nothing is. */
export function fitsChip(asset: Pick<LinkAssetInfo, 'arch'>, chip: MacChip | null): boolean {
  return chip === null || asset.arch === null || asset.arch === 'universal' || asset.arch === chip;
}

/**
 * The chip to name next to a file. Files in other formats (a Linux package, a Windows build) are not for a Mac whatever
 * chip their name carries, so the panel does not call an amd64 .deb "Intel".
 */
export function shownChip(asset: LinkAssetInfo): LinkAssetInfo['arch'] {
  return asset.format === 'other' || OTHER_SYSTEM.test(asset.name) ? null : asset.arch;
}

const ARCH_RANK = { universal: 0, arm64: 1, x86_64: 2 } as const;

/** Mac installers before other files; universal, then Apple silicon, then Intel, then files that name no chip. */
function sorted<A extends LinkAssetInfo>(assets: readonly A[]): A[] {
  const rank = (asset: A) => (isMacInstaller(asset) ? 0 : 4) + (asset.arch === null ? 3 : ARCH_RANK[asset.arch]);
  return [...assets].sort((a, b) => rank(a) - rank(b) || a.name.localeCompare(b.name));
}

export interface AssetSelection<A> {
  /** What the panel lists. */
  main: A[];
  /** What waits behind "other installers": the other chip's installers, then files for other systems. */
  rest: A[];
  /** Whether `main` holds only the Mac installers that fit. False when none could be told apart: everything is listed. */
  narrowed: boolean;
}

/**
 * The files to list for a Mac with this chip (null when it is not known: both chips are listed). When no file can be
 * positively identified as a Mac installer that fits, nothing is hidden: a developer's unusual names must not leave
 * the panel empty.
 */
export function selectAssets<A extends LinkAssetInfo>(assets: readonly A[], chip: MacChip | null): AssetSelection<A> {
  const suitable = assets.filter(asset => isMacInstaller(asset) && fitsChip(asset, chip));
  if (!suitable.length) return { main: sorted(assets), rest: [], narrowed: false };
  return { main: sorted(suitable), rest: sorted(assets.filter(asset => !suitable.includes(asset))), narrowed: true };
}
