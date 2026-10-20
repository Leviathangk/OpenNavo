// Detail display calculations (08 §10.5), pure for testing.
// Monthly installs, trends, architecture, and URL display live in shared @opennavo/ui; only web-specific calculations remain here.

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

export function packagePath(token: string, tab?: PackageSubpage): string {
  return `/apps/${token}${tab ? `/${tab}` : ''}`;
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
