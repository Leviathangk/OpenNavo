/** Release highlights support only bold and inline code; return segments for component rendering without v-html. */
export interface InlineSegment {
  type: 'text' | 'strong' | 'code';
  text: string;
}

export function parseInline(text: string): InlineSegment[] {
  const segments: InlineSegment[] = [];
  let last = 0;
  for (const match of text.matchAll(/\*\*([^*\n]+)\*\*|`([^`\n]+)`/g)) {
    const index = match.index ?? 0;
    if (index > last) segments.push({ type: 'text', text: text.slice(last, index) });
    segments.push(match[1] !== undefined ? { type: 'strong', text: match[1] } : { type: 'code', text: match[2] ?? '' });
    last = index + match[0].length;
  }
  if (last < text.length) segments.push({ type: 'text', text: text.slice(last) });
  return segments;
}

/** Patch version: at least three numeric segments with nonzero final segment (1.139.1 yes, 1.140.0 no). */
export function isPatchVersion(version: string): boolean {
  const parts = version.split('.');
  return parts.length >= 3 && parts.every(part => /^\d+$/.test(part)) && Number(parts.at(-1)) > 0;
}
