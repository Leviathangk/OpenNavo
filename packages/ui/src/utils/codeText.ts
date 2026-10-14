// Code text (08 §8.19) wraps between words, never inside a token: OnCodeText renders each run of
// non-space characters as an inline-block (.on-code-word), and Markdown inline code uses the same markup.
// A word wider than the whole line still has to break; <wbr> after path and name separators makes it
// break at "/", "." or "=" instead of at whichever letter reaches the edge.

/**
 * Runs of non-space characters and the whitespace between them, in order. Indentation at the start of a
 * line belongs to the word after it, so in pre-wrap blocks an overlong word keeps its indent instead of
 * moving down and leaving the indent behind as an empty-looking line.
 */
export function codeParts(text: string): string[] {
  const parts: string[] = [];
  let indent = '';
  for (const part of text.split(/(\s+)/)) {
    if (!part) continue;
    if (part.trim()) {
      parts.push(indent + part);
      indent = '';
      continue;
    }
    const lineStart = part.lastIndexOf('\n') + 1;
    if (!lineStart && parts.length) {
      parts.push(part);
      continue;
    }
    if (lineStart) parts.push(part.slice(0, lineStart));
    indent = part.slice(lineStart);
  }
  if (indent) parts.push(indent);
  return parts;
}

/** Pieces of one word, each ending at a separator that may start a new line. */
export function wordPieces(word: string): string[] {
  return word.match(/[^/.=]*[/.=]?/g)?.filter(Boolean) ?? [word];
}

/** HTML for Markdown inline code: each word in .on-code-word, escaped by the renderer's escaper. */
export function codeWordsHtml(text: string, escape: (value: string) => string): string {
  return codeParts(text)
    .map(part =>
      part.trim() ? `<span class="on-code-word">${wordPieces(part).map(escape).join('<wbr>')}</span>` : part
    )
    .join('');
}
