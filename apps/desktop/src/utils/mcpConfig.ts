// Local MCP client configuration presets. Every preset launches the app binary itself with the `mcp`
// subcommand, using the absolute path the running app reports (never a guessed /Applications path).

export type McpClient = 'claude' | 'cursor' | 'codex' | 'generic';

export const MCP_CLIENTS: readonly McpClient[] = ['claude', 'cursor', 'codex', 'generic'];

/** Server name the presets register; one entry per Mac is enough. */
export const MCP_SERVER_NAME = 'opennavo';

/** localStorage key holding, per client, the binary path its last copied configuration pointed at. */
export const MCP_COPIED_PATHS_KEY = 'opennavo.mcp.copiedPaths';
/** Single-slot key from before per-client tracking: it says that a configuration was copied, never for which client. */
export const MCP_COPIED_PATH_KEY = 'opennavo.mcp.copiedPath';

export type McpCopiedPaths = Partial<Record<McpClient, string>>;
/** What was recorded about copied configurations: per-client paths, plus the ambiguous pre-per-client path. */
export interface McpCopiedState {
  paths: McpCopiedPaths;
  /** Set only when the pre-per-client key was used; it cannot name a client, so it is never expanded into four. */
  legacy: string | null;
}

/** TOML basic string: escape backslashes, quotes and control characters. */
function tomlString(value: string): string {
  let out = '"';
  for (const char of value) {
    const code = char.codePointAt(0) ?? 0;
    if (char === '\\') out += '\\\\';
    else if (char === '"') out += '\\"';
    else if (code < 0x20 || code === 0x7f) out += `\\u${code.toString(16).padStart(4, '0')}`;
    else out += char;
  }
  return `${out}"`;
}

/** Configuration text for one client, ready to paste. */
export function mcpConfig(client: McpClient, commandPath: string): string {
  const server = { command: commandPath, args: ['mcp'] };
  switch (client) {
    case 'claude':
    case 'cursor':
      // claude_desktop_config.json and Cursor's mcp.json share the mcpServers shape.
      return JSON.stringify({ mcpServers: { [MCP_SERVER_NAME]: server } }, null, 2);
    case 'codex':
      // ~/.codex/config.toml
      return `[mcp_servers.${MCP_SERVER_NAME}]\ncommand = ${tomlString(commandPath)}\nargs = ["mcp"]\n`;
    case 'generic':
      return JSON.stringify(server, null, 2);
  }
}

/**
 * What was copied before, from both storage keys. The pre-per-client key could not name a client, so it is
 * kept apart instead of being copied onto all four: that made every client look stale after one move, and
 * re-copying one of them never cleared the others.
 */
export function copiedMcpState(): McpCopiedState {
  try {
    const raw = localStorage.getItem(MCP_COPIED_PATHS_KEY);
    let parsed: unknown;
    try {
      parsed = raw ? JSON.parse(raw) : undefined;
    } catch {
      // Corrupt value: keep reading the old single-slot key below rather than forgetting the copy.
      parsed = undefined;
    }
    if (parsed !== undefined) {
      const paths: McpCopiedPaths = {};
      if (parsed && typeof parsed === 'object') {
        for (const client of MCP_CLIENTS) {
          const value = (parsed as Record<string, unknown>)[client];
          if (typeof value === 'string') paths[client] = value;
        }
      }
      return { paths, legacy: null };
    }
    return { paths: {}, legacy: localStorage.getItem(MCP_COPIED_PATH_KEY) };
  } catch {
    return { paths: {}, legacy: null };
  }
}

/** Per-client paths the copied configurations used; empty when nothing was copied (or storage is off). */
export function copiedMcpPaths(): McpCopiedPaths {
  return copiedMcpState().paths;
}

export function rememberCopiedMcpPath(client: McpClient, commandPath: string): McpCopiedPaths {
  const paths = { ...copiedMcpPaths(), [client]: commandPath };
  try {
    localStorage.setItem(MCP_COPIED_PATHS_KEY, JSON.stringify(paths));
    localStorage.removeItem(MCP_COPIED_PATH_KEY);
  } catch {
    /* Without storage the moved-app hint is simply unavailable. */
  }
  return paths;
}

const tidy = (path: string) => {
  const trimmed = path.trim().normalize('NFC').replace(/\/+/g, '/');
  return trimmed.length > 1 ? trimmed.replace(/\/$/, '') : trimmed;
};

/**
 * Comparable form of a binary path: Unicode NFC, no repeated or trailing slashes, and case-folded. Case has to
 * be folded on case-insensitive volumes, where /Applications and /applications are one app, but not on
 * case-sensitive ones, where they are two directories and the second path is a move. `caseSensitive` comes
 * from the backend (`mcp_status`), which asks the volume; default false keeps the common Mac behavior.
 */
export function normalizeMcpPath(path: string, caseSensitive = false): string {
  const value = tidy(path);
  return caseSensitive ? value : value.toLowerCase();
}

/** True when a configuration was copied for a different binary path: the app has moved since. */
export function mcpAppMoved(
  commandPath: string | null,
  copied: string | null | undefined,
  caseSensitive = false
): boolean {
  return (
    commandPath !== null &&
    !!copied &&
    normalizeMcpPath(copied, caseSensitive) !== normalizeMcpPath(commandPath, caseSensitive)
  );
}

/** Clients whose copied configuration still points at an old location, in display order. */
export function mcpMovedClients(
  commandPath: string | null,
  copied: McpCopiedPaths = copiedMcpPaths(),
  caseSensitive = false
): McpClient[] {
  return MCP_CLIENTS.filter(client => mcpAppMoved(commandPath, copied[client], caseSensitive));
}

/**
 * True when the only record is the pre-per-client key and its path no longer matches: some client still points
 * there, but which one is unknown, so the page says so without naming clients it cannot identify.
 */
export function mcpLegacyStale(commandPath: string | null, legacy: string | null, caseSensitive = false): boolean {
  return commandPath !== null && !!legacy && mcpAppMoved(commandPath, legacy, caseSensitive);
}
