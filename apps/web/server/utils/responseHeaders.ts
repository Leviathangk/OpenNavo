import { getResponseHeader, setHeader, type H3Event } from 'h3';

/**
 * Comma-separated header tokens. A comma inside a quoted parameter (`title="a, b"`) separates nothing,
 * so splitting on every comma would rewrite the quoted text.
 */
function tokens(value: string): string[] {
  const parts: string[] = [];
  let current = '';
  let quoted = false;
  for (let index = 0; index < value.length; index++) {
    const char = value[index];
    if (char === '\\' && quoted && index + 1 < value.length) {
      current += char + value[++index];
      continue;
    }
    if (char === '"') quoted = !quoted;
    if (char === ',' && !quoted) {
      parts.push(current);
      current = '';
      continue;
    }
    current += char;
  }
  parts.push(current);
  return parts.map(part => part.trim()).filter(Boolean);
}

/** Add a value without discarding existing links or cache variation fields, and without repeating a token. */
export function appendHeaderValue(event: H3Event, name: string, value: string) {
  const previous = getResponseHeader(event, name);
  const values = (Array.isArray(previous) ? previous : previous ? [previous] : []).flatMap(item =>
    tokens(String(item))
  );
  for (const token of tokens(value))
    if (!values.some(item => item.toLowerCase() === token.toLowerCase())) values.push(token);
  if (values.length) setHeader(event, name, values.join(', '));
}
