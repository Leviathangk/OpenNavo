import type { ContentLocale } from '@/utils/content-locale';

// Client copies of the server's limits for an overview's search title and description, for inline
// errors only; the server trims, checks and stores them again.
export const SEO_TITLE_MAX = 70;
export const SEO_DESCRIPTION_MAX = 200;

// The server refuses what Go's unicode.IsControl matches: C0, DEL and C1.
const isControl = (char: string) => char.charCodeAt(0) < 32 || (char.charCodeAt(0) >= 127 && char.charCodeAt(0) <= 159);

/** Line breaks, tabs and other control characters become one space as they are typed or pasted. */
export function singleLine(value: string): string {
  let text = '';
  for (const char of value) {
    if (!isControl(char)) text += char;
    else if (!text.endsWith(' ')) text += ' ';
  }
  return text;
}

/** Angle brackets mean markup, which a search snippet cannot carry. */
export function hasMarkup(value: string): boolean {
  return /[<>]/.test(value);
}

/** Request body: trimmed text, null for an empty field (the template takes over again). */
export function overrideBody(locale: ContentLocale, form: { title: string; description: string }) {
  return { locale, title: form.title.trim() || null, description: form.description.trim() || null };
}
