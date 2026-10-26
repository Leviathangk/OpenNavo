/** Homebrew client install command shared by download and landing pages. */
export const DESKTOP_BREW_COMMAND = 'brew install --cask opennavo';

/** Only an explicitly enabled, published cask may be offered. */
export function desktopBrewCaskAvailable(config: Record<string, unknown>): boolean {
  return config.desktopBrewCaskAvailable === true || config.desktopBrewCaskAvailable === 'true';
}
