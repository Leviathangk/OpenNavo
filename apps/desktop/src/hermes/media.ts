/** Media delivered by Hermes in a `MEDIA:` tag. Mirrors the official desktop parser. */

export type HermesMediaKind = 'audio' | 'image' | 'video' | 'file';

export interface HermesMediaPart {
  type: 'media';
  path: string;
  kind: HermesMediaKind;
  name: string;
}

export interface HermesTextPart {
  type: 'text';
  text: string;
}

export type HermesContentPart = HermesTextPart | HermesMediaPart;

/**
 * Known deliverable extensions. The anchored branch uses this list so an unquoted path may
 * contain spaces; unknown extensions still work when they have no spaces (bare fallback).
 */
const MEDIA_DELIVERY_EXTS = [
  'png',
  'jpg',
  'jpeg',
  'gif',
  'webp',
  'bmp',
  'tiff',
  'svg',
  'heic',
  'heif',
  'avif',
  'mp4',
  'm4v',
  'mov',
  'avi',
  'mkv',
  'webm',
  '3gp',
  'mp3',
  'm2a',
  'wav',
  'ogg',
  'opus',
  'm4a',
  'flac',
  'aac',
  'pdf',
  'docx',
  'doc',
  'odt',
  'rtf',
  'txt',
  'md',
  'epub',
  'xlsx',
  'xls',
  'ods',
  'csv',
  'tsv',
  'json',
  'xml',
  'yaml',
  'yml',
  'kmz',
  'kml',
  'geojson',
  'gpx',
  'pptx',
  'ppt',
  'odp',
  'key',
  'zip',
  'tar',
  'gz',
  'tgz',
  'bz2',
  'xz',
  '7z',
  'rar',
  'apk',
  'ipa',
  'html',
  'htm'
] as const;

const MEDIA_KIND_BY_EXT: Record<string, HermesMediaKind> = {
  png: 'image',
  jpg: 'image',
  jpeg: 'image',
  gif: 'image',
  webp: 'image',
  bmp: 'image',
  tiff: 'image',
  svg: 'image',
  heic: 'image',
  heif: 'image',
  avif: 'image',
  mp4: 'video',
  m4v: 'video',
  mov: 'video',
  avi: 'video',
  mkv: 'video',
  webm: 'video',
  '3gp': 'video',
  mp3: 'audio',
  m2a: 'audio',
  wav: 'audio',
  ogg: 'audio',
  opus: 'audio',
  m4a: 'audio',
  flac: 'audio',
  aac: 'audio'
};

const MEDIA_DELIVERY_EXT_SET = new Set<string>(MEDIA_DELIVERY_EXTS);

function caseInsensitiveLiteral(value: string): string {
  return [...value]
    .map(char => {
      const lower = char.toLowerCase();
      const upper = char.toUpperCase();
      return lower === upper ? char : `[${lower}${upper}]`;
    })
    .join('');
}

const MEDIA_EXT_PATTERN = [...MEDIA_DELIVERY_EXTS]
  .sort((left, right) => right.length - left.length)
  .map(caseInsensitiveLiteral)
  .join('|');

// Chinese-language agent output often appends full-width punctuation or size hints directly to a
// path (`早报.pdf（782.6 KB）`). The official parser accepts these as path terminators, and so must
// the renderer or the attachment disappears.
const MEDIA_CJK_TERMINATORS = '（）〈〉《》：，。；！？、“”‘’【】';
const MEDIA_PATH_BOUNDARY = `\\s\`"'*_,;:)\\]}${MEDIA_CJK_TERMINATORS}`;

const MEDIA_PATH_ANCHORED = `(?:~/|/|[A-Za-z]:[/\\\\])\\S+?(?:[^\\S\\n]+\\S+?)*?\\.(?:${MEDIA_EXT_PATTERN})(?=[${MEDIA_PATH_BOUNDARY}]|MEDIA:|$)`;
const MEDIA_PATH_BARE = `[^\\s\`"'${MEDIA_CJK_TERMINATORS}]+`;
const MEDIA_TRAILING_PUNCTUATION = `.,;:!?${MEDIA_CJK_TERMINATORS}`;

const MEDIA_TAG_RE = new RegExp(
  `[\`"']?MEDIA:\\s*(?<inline>\`[^\`\\n]+\`|"[^"\\n]+"|'[^'\\n]+'|${MEDIA_PATH_ANCHORED}|${MEDIA_PATH_BARE})[\`"']?`,
  'g'
);

function isPlausibleMediaPath(value: string): boolean {
  return value.includes('/') || value.includes('\\') || /\.[^.]/.test(value);
}

function unquoteMediaPath(value: string): string {
  const trimmed = value.trim();
  const quote = trimmed[0];
  if (quote && quote === trimmed.at(-1) && ['"', "'", '`'].includes(quote)) {
    return trimmed.slice(1, -1);
  }
  const last = trimmed.at(-1);
  return last === '`' || last === '"' ? trimmed.slice(0, -1) : trimmed;
}

function splitTrailingPunctuation(value: string): { path: string; punctuation: string } {
  let end = value.length;
  while (end > 0 && MEDIA_TRAILING_PUNCTUATION.includes(value[end - 1] ?? '')) {
    if (!isPlausibleMediaPath(value.slice(0, end - 1))) break;
    end -= 1;
  }
  return { path: value.slice(0, end), punctuation: value.slice(end) };
}

function extensionSuffix(path: string): string {
  const name = mediaName(path);
  const dot = name.lastIndexOf('.');
  // A leading dot is a dotfile (`.env`), never an extension (`.png` names no image). A trailing
  // dot is punctuation, not an extension.
  if (dot <= 0 || dot === name.length - 1) return '';
  return name.slice(dot + 1);
}

/**
 * A streamed tag can arrive before its extension does (`…/sample-scene.p`). Requiring a
 * file extension keeps that partial path as text instead of flashing a file card.
 */
function hasDeliverableExtension(path: string): boolean {
  const name = mediaName(path);
  const dot = name.lastIndexOf('.');
  // A dotfile such as `.env` or `.gitignore` is a complete filename, not an extension-less
  // stream. It stays a file card; `.png` itself is a file, never an image.
  if (dot === 0 && name.length > 1 && dot !== name.length - 1) return true;
  const suffix = extensionSuffix(path);
  return /^[A-Za-z0-9]{1,12}$/.test(suffix);
}

/**
 * True while the trailing suffix is still a prefix of a known extension. Only the very end
 * of the stream is held back; once punctuation, whitespace or another character arrives the
 * path is a complete token again.
 */
function hasPartialKnownExtension(path: string): boolean {
  const name = mediaName(path);
  if (name.startsWith('.') && !name.slice(1).includes('.')) {
    const tail = name.slice(1).toLowerCase();
    if (!tail || tail.length > 12 || MEDIA_DELIVERY_EXT_SET.has(tail)) return false;
    return MEDIA_DELIVERY_EXTS.some(extension => extension.startsWith(tail));
  }
  const suffix = extensionSuffix(path).toLowerCase();
  if (!suffix || suffix.length > 12 || MEDIA_DELIVERY_EXT_SET.has(suffix)) return false;
  return MEDIA_DELIVERY_EXTS.some(extension => extension.startsWith(suffix));
}

function normalizedCapture(
  value: string,
  atTextEnd: boolean,
  streaming: boolean
): { path: string; punctuation: string } | null {
  const raw = value.trim();
  const quote = raw[0];
  const quoted = quote !== undefined && quote === raw.at(-1) && ['"', "'", '`'].includes(quote);
  const cleaned = quoted
    ? { path: unquoteMediaPath(raw), punctuation: '' }
    : splitTrailingPunctuation(unquoteMediaPath(raw));
  if (!cleaned.path || !isPlausibleMediaPath(cleaned.path)) return null;
  // Quotes are the escape hatch for odd names (`MEDIA:"/tmp/a b.md."`). An unquoted
  // path still needs an extension so a streamed prefix does not flash a file card.
  if (!quoted && !hasDeliverableExtension(cleaned.path)) return null;
  if (streaming && !quoted && atTextEnd && !cleaned.punctuation && hasPartialKnownExtension(cleaned.path)) {
    return null;
  }
  return cleaned;
}

function extensionOf(path: string): string {
  const trimmed = path.trim();
  const isUrl = /^[a-z][a-z0-9+.-]*:/i.test(trimmed) && !/^[a-z]:[\\/]/i.test(trimmed);
  const value = isUrl ? (trimmed.split(/[?#]/, 1)[0] ?? '') : trimmed;
  const name = value.split(/[\\/]/).pop() ?? '';
  const dot = name.lastIndexOf('.');
  if (dot <= 0 || dot === name.length - 1) return '';
  return name.slice(dot + 1).toLowerCase();
}

export function mediaKind(path: string): HermesMediaKind {
  return MEDIA_KIND_BY_EXT[extensionOf(path)] ?? 'file';
}

export function mediaName(path: string): string {
  const trimmed = path.trim();
  if (!/^[A-Za-z]:[\\/]/.test(trimmed)) {
    try {
      const name = new URL(trimmed).pathname.split('/').filter(Boolean).pop();
      if (name) {
        try {
          return decodeURIComponent(name);
        } catch {
          return name;
        }
      }
    } catch {
      // A native path, not a URL.
    }
  }
  return trimmed.split(/[\\/]/).filter(Boolean).pop() || trimmed;
}

/** A URL that must only be opened by an explicit user action, never fetched by the webview. */
export function isExternalMediaSrc(path: string): boolean {
  return /^https?:\/\//i.test(path.trim());
}

function boundaryText(raw: string, previousIsMedia: boolean, nextIsMedia: boolean): string {
  let value = raw.replace(/\r\n/g, '\n');
  if (previousIsMedia) value = value.replace(/^[\t ]*\n+/, '');
  if (nextIsMedia) value = value.replace(/\n+[\t ]*$/, '');
  value = value.replace(/\n{3,}/g, '\n\n');
  return value;
}

interface TextSpan {
  start: number;
  end: number;
}

const FENCED_CODE_RE = /(```|~~~)[^\n]*\n[\s\S]*?\1/g;
const INLINE_CODE_RE = /`[^`\n]+`/g;
const BLOCKQUOTE_RE = /^[ \t]{0,3}>.*$/gm;
// No lookbehind: macOS 13.0–13.2 ships WKWebView 16.1–16.3, where a lookbehind makes the whole
// module fail to parse and takes the Assistant page down. Capture the delimiter instead.
const JSON_STRING_VALUE_RE = /[:,{[]\s*"((?:[^"\\\n]|\\.)*)"/g;
const JSON_MEDIA_PATH_RE = /MEDIA:\s*(?:~\/|\/|[A-Za-z]:[\\/])/;

/** Blank protected text while preserving offsets, so matches still slice the original string. */
function blankSpans(content: string, spans: TextSpan[]): string {
  if (!spans.length) return content;
  const chars = content.split('');
  for (const { start, end } of spans) {
    for (let index = Math.max(0, start); index < Math.min(chars.length, end); index += 1) {
      if (chars[index] !== '\n') chars[index] = ' ';
    }
  }
  return chars.join('');
}

/** An inline code span is a delivery only when the whole span is one complete `MEDIA:` tag. */
function isExactInlineMediaTag(span: string): boolean {
  const inner = span.slice(1, -1).trim();
  if (!/^MEDIA:\s*\S/.test(inner)) return false;
  const matches = [...inner.matchAll(MEDIA_TAG_RE)];
  return matches.length === 1 && matches[0]?.[0] === inner;
}

function maskProtectedSpans(content: string): string {
  const spans: TextSpan[] = [];
  for (const match of content.matchAll(FENCED_CODE_RE)) {
    const start = match.index ?? 0;
    spans.push({ start, end: start + match[0].length });
  }
  for (const match of content.matchAll(INLINE_CODE_RE)) {
    const start = match.index ?? 0;
    // A backtick directly after `MEDIA:` is a quote around the path, not an inline code sample.
    if (/MEDIA:\s*$/.test(content.slice(Math.max(0, start - 20), start))) continue;
    // Models also wrap a real delivery in inline code: `MEDIA:/tmp/report.pdf`. Keep a span
    // that is exactly one tag; the backend still validates the path before it is rendered.
    if (isExactInlineMediaTag(match[0])) continue;
    spans.push({ start, end: start + match[0].length });
  }
  for (const match of content.matchAll(BLOCKQUOTE_RE)) {
    const start = match.index ?? 0;
    spans.push({ start, end: start + match[0].length });
  }
  return blankSpans(content, spans);
}

function maskJsonStringMedia(content: string): string {
  if (!content.includes('"') || !content.includes('MEDIA:')) return content;
  const spans: TextSpan[] = [];
  for (const match of content.matchAll(JSON_STRING_VALUE_RE)) {
    const body = match[1] ?? '';
    if (!JSON_MEDIA_PATH_RE.test(body)) continue;
    const start = (match.index ?? 0) + match[0].length - body.length - 1;
    spans.push({ start, end: start + body.length });
  }
  return blankSpans(content, spans);
}

/**
 * Split assistant text into ordered prose and media parts. A `MEDIA:` tag that does not name a
 * plausible file stays in the prose, so streamed or malformed tags never disappear.
 */
export function parseHermesMedia(text: string, streaming = false): HermesContentPart[] {
  if (!text) return [];
  // Locate tags on an offset-preserving masked copy: examples in code fences, blockquotes and
  // serialized JSON tool output must stay visible as prose, never turn into live file cards.
  const scanText = maskJsonStringMedia(maskProtectedSpans(text));

  const parts: HermesContentPart[] = [];
  let cursor = 0;
  let pending = '';

  const pushText = (value: string) => {
    if (!value) return;
    parts.push({ type: 'text', text: value });
  };

  for (const match of scanText.matchAll(MEDIA_TAG_RE)) {
    const index = match.index ?? 0;
    const capture = match.groups?.inline ?? match[1] ?? '';
    const captureStart = index + match[0].indexOf(capture);
    const cleaned = normalizedCapture(capture, captureStart + capture.length >= text.length, streaming);
    if (!cleaned) continue;

    pushText(pending + text.slice(cursor, index));
    pending = cleaned.punctuation;
    parts.push({
      type: 'media',
      path: cleaned.path,
      kind: mediaKind(cleaned.path),
      name: mediaName(cleaned.path)
    });
    let end = index + match[0].length;
    if (cleaned.punctuation && text.startsWith(cleaned.punctuation, end)) {
      end += cleaned.punctuation.length;
    }
    cursor = end;
  }

  pushText(pending + text.slice(cursor));

  if (!parts.some(part => part.type === 'media')) {
    return [{ type: 'text', text }];
  }

  const separated = parts.filter((part, index) => {
    if (part.type !== 'text') return true;
    const previousIsMedia = parts[index - 1]?.type === 'media';
    const nextIsMedia = parts[index + 1]?.type === 'media';
    part.text = boundaryText(part.text, previousIsMedia, nextIsMedia);
    return part.text.length > 0;
  });

  return separated.length ? separated : [{ type: 'text', text }];
}

export function hasHermesMedia(text: string, streaming = false): boolean {
  return parseHermesMedia(text, streaming).some(part => part.type === 'media');
}
