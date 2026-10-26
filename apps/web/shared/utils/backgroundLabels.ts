const encoder = new TextEncoder();
const byteLength = (text: string) => encoder.encode(text).length;

/**
 * A label the API can look up (facts.ValidBackgroundLabel): 1–255 bytes, no surrounding spaces, no
 * slash, backslash, ? or #, and no control characters. Anything else is a 404 before any request.
 */
export function isValidLabel(label: string): boolean {
  const bytes = byteLength(label);
  return bytes > 0 && bytes <= 255 && label.trim() === label && !/[/\\?#]/.test(label) && !/\p{Cc}/u.test(label);
}

/**
 * The page path of a label. Characters a path segment may hold stay as they are (homebrew.mxcl.postgresql@16
 * keeps its @), Markdown delimiters and everything else are percent-encoded. An optional locale root
 * comes from localePath('/'); passing the label itself through localePath would undo these escapes.
 */
export function labelPath(label: string, localeRoot = ''): string {
  const segment = encodeURIComponent(label)
    .replace(/[*!'()]/g, char => `%${char.charCodeAt(0).toString(16).toUpperCase()}`)
    .replace(/%(40|2B|3A)/gi, (_, hex: string) => String.fromCharCode(Number.parseInt(hex, 16)));
  return `${localeRoot.replace(/\/$/, '')}/background-items/${segment}`;
}

/** Decode exactly one URL segment; encoded separators and malformed escapes are never labels. */
export function decodeLabelSegment(segment: string): string | undefined {
  try {
    const label = decodeURIComponent(segment);
    return isValidLabel(label) ? label : undefined;
  } catch {
    return undefined;
  }
}
