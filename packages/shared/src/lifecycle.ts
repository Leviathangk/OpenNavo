/** Homebrew's own lifecycle for a package, as the API reports it (PackageDetail.homebrewStatus). */
export interface HomebrewLifecycle {
  state: string;
}

/**
 * Whether Homebrew itself disabled the package. The catalog's disabled flag also covers apps that
 * OpenNavo's 90-day rule paused, which Homebrew still installs; only Homebrew's status tells the two
 * apart. Without that status (older APIs, the local catalog) the flag keeps its old meaning.
 */
export function disabledByHomebrew(pkg: { disabled?: boolean; homebrewStatus?: HomebrewLifecycle | null }): boolean {
  if (!pkg.disabled) return false;
  return pkg.homebrewStatus ? pkg.homebrewStatus.state === 'disabled' : true;
}
