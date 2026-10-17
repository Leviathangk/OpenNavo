// Brewfile generation/parsing (05 §7.2, 06 §6.3): data only, never execute Ruby, which Brewfiles actually contain.
// Recognize only linewise brew "x" and cask "y"; report other entries as unsupported for the UI.
import { isValidToken } from './commands';
import type { PackageKind } from './types';

export interface BrewfileItem {
  kind: PackageKind;
  token: string;
}

export interface BrewfileEntry extends BrewfileItem {
  /** One-based line number. */
  line: number;
  /** Extra arguments such as args:/restart_service: are ignored on import. */
  hasOptions: boolean;
}

export type BrewfileUnsupportedReason =
  /** Unofficial taps or third-party brew "user/tap/name" packages. */
  | 'third-party'
  /** Other package managers such as mas, vscode, whalebrew. */
  | 'other-manager'
  /** Package name violates the allowlist. */
  | 'invalid-token'
  /** Unrecognized line, including Ruby logic or syntax errors. */
  | 'unrecognized';

export interface BrewfileUnsupported {
  line: number;
  text: string;
  reason: BrewfileUnsupportedReason;
}

export interface BrewfileParseResult {
  entries: BrewfileEntry[];
  unsupported: BrewfileUnsupported[];
}

// Link apps have no Homebrew line; toBrewfile skips them.
const KIND_KEYWORD: Record<Exclude<PackageKind, 'link'>, string> = { cask: 'cask', formula: 'brew' };

// Official Homebrew taps need no action.
const OFFICIAL_TAPS = new Set(['homebrew/core', 'homebrew/cask', 'homebrew/bundle', 'homebrew/services']);

const ENTRY = /^(brew|cask|tap|mas|vscode|whalebrew|go|cargo|uv|npm|flatpak)\s+(["'])(.*?)\2\s*(,.*)?$/;

function stripComment(line: string): string {
  // Only # outside quotes starts a comment.
  let quote = '';
  for (let index = 0; index < line.length; index += 1) {
    const char = line.charAt(index);
    if (quote) {
      if (char === quote) quote = '';
    } else if (char === '"' || char === "'") {
      quote = char;
    } else if (char === '#') {
      return line.slice(0, index);
    }
  }
  return line;
}

/** Generate optional title comment plus ordered entries; skip invalid tokens and duplicates. */
export function toBrewfile(items: readonly BrewfileItem[], { title }: { title?: string } = {}): string {
  const seen = new Set<string>();
  const lines: string[] = [];
  if (title) lines.push(`# ${title.replace(/\s+/g, ' ').trim()}`);
  for (const { kind, token } of items) {
    const key = `${kind}:${token}`;
    if (kind === 'link' || !isValidToken(token) || seen.has(key)) continue;
    seen.add(key);
    lines.push(`${KIND_KEYWORD[kind]} "${token}"`);
  }
  return `${lines.join('\n')}\n`;
}

/** Parse Brewfiles into deduplicated installable entries and unsupported lines. */
export function parseBrewfile(text: string): BrewfileParseResult {
  const entries: BrewfileEntry[] = [];
  const unsupported: BrewfileUnsupported[] = [];
  const seen = new Set<string>();

  text.split(/\r?\n/).forEach((raw, index) => {
    const line = index + 1;
    const content = stripComment(raw).trim();
    if (!content) return;

    const match = ENTRY.exec(content);
    if (!match) {
      unsupported.push({ line, text: raw.trim(), reason: 'unrecognized' });
      return;
    }
    const [, keyword = '', , value = '', options] = match;

    if (keyword === 'tap') {
      if (!OFFICIAL_TAPS.has(value.toLowerCase())) unsupported.push({ line, text: raw.trim(), reason: 'third-party' });
      return;
    }
    if (keyword !== 'brew' && keyword !== 'cask') {
      unsupported.push({ line, text: raw.trim(), reason: 'other-manager' });
      return;
    }

    const kind: PackageKind = keyword === 'cask' ? 'cask' : 'formula';
    // Fully qualified official names (homebrew/core/wget, homebrew/cask/firefox) equal short names.
    const segments = value.split('/');
    let token = value;
    if (segments.length > 1) {
      if (segments.length !== 3 || !OFFICIAL_TAPS.has(`${segments[0]}/${segments[1]}`.toLowerCase())) {
        unsupported.push({ line, text: raw.trim(), reason: 'third-party' });
        return;
      }
      token = value.slice(value.lastIndexOf('/') + 1);
    }
    if (!isValidToken(token)) {
      unsupported.push({ line, text: raw.trim(), reason: 'invalid-token' });
      return;
    }

    const key = `${kind}:${token}`;
    if (seen.has(key)) return;
    seen.add(key);
    entries.push({ kind, token, line, hasOptions: options !== undefined });
  });

  return { entries, unsupported };
}
