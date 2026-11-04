/**
 * Curated package pages linked from the homepage, download page and every language landing page
 * (#214). The links render on the server so crawlers find a direct path into the catalog before any
 * island hydrates. Product names are not translated; `dotnet-sdk` is the documented discovery pilot.
 */
export interface PackageGuideLink {
  token: string;
  name: string;
}

export const PACKAGE_GUIDE_LINKS: readonly PackageGuideLink[] = [
  { token: 'dotnet-sdk', name: '.NET SDK' },
  { token: 'visual-studio-code', name: 'Visual Studio Code' },
  { token: 'ghostty', name: 'Ghostty' },
  { token: 'docker-desktop', name: 'Docker Desktop' },
  { token: 'raycast', name: 'Raycast' },
  { token: 'obsidian', name: 'Obsidian' }
];
