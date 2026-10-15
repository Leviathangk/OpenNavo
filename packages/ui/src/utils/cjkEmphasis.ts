import type { MarkdownIt } from 'markdown-it';

// Chinese, Japanese and Korean script, their punctuation and fullwidth forms.
const CJK =
  /[\p{Script=Han}\p{Script=Hiragana}\p{Script=Katakana}\p{Script=Hangul}\p{Script=Bopomofo}\u3000-\u303f\uff00-\uffef]/u;
const isCjk = (code: number) => CJK.test(String.fromCodePoint(code));

/** The code point before index; the start of the text counts as a space, as in CommonMark. */
function codePointBefore(src: string, index: number): number {
  if (index <= 0) return 0x20;
  const low = src.charCodeAt(index - 1);
  if (low >= 0xdc00 && low <= 0xdfff && index >= 2) {
    const high = src.charCodeAt(index - 2);
    if (high >= 0xd800 && high <= 0xdbff) return (high - 0xd800) * 0x400 + (low - 0xdc00) + 0x10000;
  }
  return low;
}

/**
 * CommonMark lets a ** run beside punctuation open or close only next to whitespace or more punctuation.
 * CJK text has no spaces, so "要求 macOS 27 或更新版本。**Intel" never closed and the asterisks showed.
 * In the spirit of the CJK-friendly amendment, punctuation stops no run when either neighbor is CJK;
 * runs between other characters keep CommonMark's rules.
 */
export function cjkFriendlyEmphasis(md: MarkdownIt): void {
  const { isMdAsciiPunct, isPunctCharCode, isWhiteSpace } = md.utils;
  md.inline.State = class extends md.inline.State {
    override scanDelims(start: number, canSplitWord: boolean) {
      const scanned = super.scanDelims(start, canSplitWord);
      const last = codePointBefore(this.src, start);
      const end = start + scanned.length;
      const next = end < this.posMax ? (this.src.codePointAt(end) ?? 0x20) : 0x20;
      if (!isCjk(last) && !isCjk(next)) return scanned;
      const left = !isWhiteSpace(next);
      const right = !isWhiteSpace(last);
      const lastPunct = isMdAsciiPunct(last) || isPunctCharCode(last);
      const nextPunct = isMdAsciiPunct(next) || isPunctCharCode(next);
      return {
        ...scanned,
        can_open: left && (canSplitWord || !right || lastPunct),
        can_close: right && (canSplitWord || !left || nextPunct)
      };
    }
  };
}
