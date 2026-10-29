import { installCommand, isValidToken } from '@opennavo/shared';
import type { Kind } from '@/ipc/bindings';

interface BrewIdentity {
  tap?: string | null;
  brewToken?: string | null;
}

/** Resolve catalog aliases only when constructing text meant for Homebrew. */
export function catalogInstallCommand(kind: Exclude<Kind, 'link'>, token: string, item?: BrewIdentity | null) {
  return installCommand(kind, item?.brewToken ?? token, item?.tap);
}

export function catalogBrewfile(
  items: readonly { kind: Kind; token: string }[],
  lookup: (kind: Kind, token: string) => BrewIdentity | null | undefined,
  title: string
) {
  const lines = [`# ${title.replace(/\s+/g, ' ').trim()}`];
  const seen = new Set<string>();
  for (const { kind, token } of items) {
    if (kind === 'link' || !isValidToken(token)) continue;
    const command = catalogInstallCommand(kind, token, lookup(kind, token));
    const identity = command.slice(command.lastIndexOf(' ') + 1);
    const key = `${kind}:${identity}`;
    if (seen.has(key)) continue;
    seen.add(key);
    lines.push(`${kind === 'cask' ? 'cask' : 'brew'} "${identity}"`);
  }
  return `${lines.join('\n')}\n`;
}
