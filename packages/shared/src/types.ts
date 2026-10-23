/** `link`: software offered only through its official site or GitHub, never installed by Homebrew (store/security plan §4). */
export type PackageKind = 'cask' | 'formula' | 'link';

export const PACKAGE_KINDS: readonly PackageKind[] = ['cask', 'formula', 'link'];

export function isPackageKind(value: unknown): value is PackageKind {
  return value === 'cask' || value === 'formula' || value === 'link';
}

/** Kinds Homebrew installs; link apps only open their download page. */
export const isInstallableKind = (kind: PackageKind): kind is 'cask' | 'formula' => kind !== 'link';

/** App locales (six): admin UI and server messages; the website also indexes them without verified translations. */
export type { LocaleCode as Locale } from './locales.gen';
export { LOCALES, APP_LOCALES } from './locales.gen';
/** Mac client locales (ten): its UI, native menus, downloaded catalog data and release notes. */
export type { DesktopLocaleCode as DesktopLocale } from './locales.gen';
export { DESKTOP_LOCALES } from './locales.gen';
/** Website and content locales (ten): the app locales plus de-DE, fr-FR, it-IT and zh-Hant. */
export type { SiteLocaleCode as SiteLocale } from './locales.gen';
export { SITE_LOCALES } from './locales.gen';
