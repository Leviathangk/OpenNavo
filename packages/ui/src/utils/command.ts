// Long install commands (08 §8.19) wrap after --cask/--formula using a space/backslash and an indented continuation,
// never inside tokens. OnInstallCommand only wraps when the command does not fit its row.

/** Split point of a command longer than wrapAt: through --cask/--formula, then the rest; null keeps one line. */
export function commandBreak(command: string, wrapAt = 30): { head: string; tail: string } | null {
  const text = command.trim();
  if (text.length <= wrapAt) return null;
  const match = /^(.*?--(?:cask|formula))\s+(.+)$/.exec(text);
  return match ? { head: match[1]!, tail: match[2]! } : null;
}
