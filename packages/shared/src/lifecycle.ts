/** Homebrew's own lifecycle for a package, as the API reports it (PackageDetail.homebrewStatus). */
export interface HomebrewLifecycle {
  state: string;
  reason?: string | null;
}

export type PackageInstallState = 'active' | 'disabled' | 'deprecated' | 'paused';

/** Homebrew disable blocks installation; OpenNavo's recommendation pause does not. */
export function packageInstallState(pkg: {
  disabled?: boolean;
  deprecated?: boolean;
  disable?: { reason?: string | null } | null;
  deprecation?: { reason?: string | null } | null;
  recommendationPaused?: boolean;
  homebrewStatus?: HomebrewLifecycle | null;
}): PackageInstallState {
  const state = pkg.homebrewStatus?.state;
  if (state === 'disabled' || (!state && (pkg.disabled || pkg.disable))) return 'disabled';
  if (state === 'deprecated' || pkg.deprecated || pkg.deprecation) return 'deprecated';
  if (pkg.recommendationPaused || pkg.disable || pkg.disabled) return 'paused';
  return 'active';
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
