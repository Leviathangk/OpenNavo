// brew commands (06 §6.3): Rust constructs execution argument arrays; these are for dialogs, clipboard, and web display.
import type { PackageKind } from './types';

/** Token allowlist (06 §6.3): reject spaces, quotes, slashes, and any shell-interpretable characters. */
export const TOKEN_PATTERN = /^[a-z0-9][a-z0-9@._+-]{0,127}$/;

export function isValidToken(token: string): boolean {
  return TOKEN_PATTERN.test(token);
}

export class InvalidTokenError extends Error {
  constructor(token: string) {
    super(`Invalid package identifier: ${JSON.stringify(token)}`);
    this.name = 'InvalidTokenError';
  }
}

export type BrewOperation =
  | { op: 'install'; kind: PackageKind; token: string; adopt?: boolean }
  | { op: 'upgrade'; kind: PackageKind; token: string }
  | { op: 'uninstall'; kind: PackageKind; token: string; zap?: boolean; force?: boolean }
  | { op: 'reinstall' | 'pin' | 'unpin'; kind: PackageKind; token: string };

/** Arguments: explicitly pass --cask or --formula; explicit Cask upgrades always use --greedy. */
export function brewArgs(operation: BrewOperation): string[] {
  const { op, kind, token } = operation;
  if (!isValidToken(token)) throw new InvalidTokenError(token);
  const flags = [`--${kind}`];
  if (op === 'upgrade' && kind === 'cask') flags.push('--greedy');
  if (op === 'install' && kind === 'cask' && operation.adopt) flags.push('--adopt');
  if (op === 'uninstall' && kind === 'cask' && operation.zap) flags.push('--zap');
  // Use --force to finish retries after partial zap failure (06 §6.3).
  if (op === 'uninstall' && kind === 'cask' && operation.force) flags.push('--force');
  return [op, ...flags, token];
}

export function formatBrewCommand(args: readonly string[]): string {
  return ['brew', ...args].join(' ');
}

/** Third-party tap names (owner/name) as the catalog lists them; Homebrew's own taps never qualify a token. */
export const TAP_PATTERN = /^[a-z0-9][a-z0-9-]{0,38}\/[a-z0-9][a-z0-9_.-]{0,99}$/;

export function isThirdPartyTap(tap: string | null | undefined): tap is string {
  return Boolean(tap) && !tap!.startsWith('homebrew/');
}

/** Install command, matching server installCommand rules (04 §4); casks from a third-party tap use `<tap>/<token>`. */
export function installCommand(kind: PackageKind, token: string, tap?: string | null): string {
  const args = brewArgs({ op: 'install', kind, token });
  if (kind === 'cask' && isThirdPartyTap(tap)) {
    if (!TAP_PATTERN.test(tap)) throw new InvalidTokenError(`${tap}/${token}`);
    args[args.length - 1] = `${tap}/${token}`;
  }
  return formatBrewCommand(args);
}
