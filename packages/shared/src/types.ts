/** `link`: software offered only through its official site or GitHub, never installed by Homebrew (store/security plan §4). */
export type PackageKind = 'cask' | 'formula' | 'link';

export const PACKAGE_KINDS: readonly PackageKind[] = ['cask', 'formula', 'link'];

export function isPackageKind(value: unknown): value is PackageKind {
  return value === 'cask' || value === 'formula' || value === 'link';
}

/** Kinds Homebrew installs; link apps only open their download page. */
export const isInstallableKind = (kind: PackageKind): kind is 'cask' | 'formula' => kind !== 'link';

/** UI locales generated from the six-language manifest. */
export type { LocaleCode as Locale } from './locales.gen';
export { LOCALES } from './locales.gen';
