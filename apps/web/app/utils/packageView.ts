// Detail display calculations (08 §10.5), pure for testing.
// Monthly installs, trends, architecture, and URL display live in shared @opennavo/ui; only web-specific calculations remain here.
import { BRAND, LOCALE_METADATA, SITE_LOCALES } from '@opennavo/shared';

/** Detail URLs: Casks (ADR-018) and link apps share /apps; their tokens never collide. */
/** Pages under an app: its tabs, the troubleshooting page (F7) and the intent pages (X2). */
export const PACKAGE_SUBPAGES = [
  'versions',
  'dependencies',
  'details',
  'troubleshooting',
  'alternatives',
  'uninstall'
] as const;
export type PackageSubpage = (typeof PACKAGE_SUBPAGES)[number];

/**
 * OpenNavo's own catalog entry is the desktop app, whose page is /download: its overview answers a 301
 * there (server/middleware/package-canonical.ts), so links go straight to it. Its tabs stay where they are.
 */
export function packagePath(token: string, tab?: PackageSubpage): string {
  if (!tab && token === BRAND.caskToken) return '/download';
  return `/apps/${token}${tab ? `/${tab}` : ''}`;
}

const BRAND_OVERVIEW = `/apps/${BRAND.caskToken}`;
/** Language prefixes of the site's paths (/zh, /zh-hant, …; English has none). */
const LOCALE_PREFIXES: ReadonlySet<string> = new Set(SITE_LOCALES.map(locale => LOCALE_METADATA[locale].prefix));

/**
 * Where OpenNavo's own overview goes in its language, for the 301 and for canonicals that name it:
 * /apps/opennavo → /download, /zh/apps/opennavo → /zh/download. Undefined for every other path.
 */
export function brandDownloadPath(path: string): string | undefined {
  if (!path.endsWith(BRAND_OVERVIEW)) return undefined;
  const prefix = path.slice(0, -BRAND_OVERVIEW.length);
  return LOCALE_PREFIXES.has(prefix) ? `${prefix}/download` : undefined;
}

/** Mixed dependency/conflict/alternative lists: Casks and link apps have detail links; command-line tools show names only. */
export function packageLink(kind: string, token: string): string | undefined {
  return kind === 'cask' || kind === 'link' ? packagePath(token) : undefined;
}

/** Map categories to schema.org applicationCategory (05 §6.2, T4), defaulting to UtilitiesApplication. */
const APPLICATION_CATEGORY: Record<string, string> = {
  'developer-tools': 'DeveloperApplication',
  terminal: 'DeveloperApplication',
  languages: 'DeveloperApplication',
  databases: 'DeveloperApplication',
  virtualization: 'DeveloperApplication',
  design: 'DesignApplication',
  media: 'MultimediaApplication',
  communication: 'CommunicationApplication',
  games: 'GameApplication',
  security: 'SecurityApplication',
  education: 'EducationalApplication',
  browsers: 'BrowserApplication',
  drivers: 'DriverApplication',
  'menu-bar': 'DesktopEnhancementApplication',
  office: 'BusinessApplication',
  productivity: 'BusinessApplication',
  writing: 'BusinessApplication'
};

/**
 * schema.org applicationCategory from the primary category, else the first of the app's other categories
 * that has one (subcategories and unmapped primaries fall through to a mapped one).
 */
export function applicationCategory(slug: string | null | undefined, others: readonly string[] = []): string {
  for (const candidate of [slug, ...others]) {
    const category = candidate ? APPLICATION_CATEGORY[candidate] : undefined;
    if (category) return category;
  }
  return 'UtilitiesApplication';
}
