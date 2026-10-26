import { decodeLabelSegment, labelPath } from '#shared/utils/backgroundLabels';

/**
 * The path as canonicals, hreflang and sitemaps spell it: each segment decoded, then written with encodeURI,
 * so app tokens keep their literal + and @ (plus-app++, not plus-app%2B%2B, which the server moves back with a
 * 301) while spaces and other characters stay escaped. A slash, ? or # inside a segment stays escaped too.
 */
export function canonicalPath(path: string): string {
  const parts = path.split('/');
  const at = parts.indexOf('background-items');
  if ((at === 1 || at === 2) && parts.length === at + 2) {
    const label = decodeLabelSegment(parts[at + 1]!);
    if (label !== undefined) return labelPath(label, parts.slice(0, at).join('/'));
  }
  return path
    .split('/')
    .map(segment => {
      let decoded: string;
      try {
        decoded = decodeURIComponent(segment);
      } catch {
        return segment;
      }
      return encodeURI(decoded).replace(/[/?#]/g, char => encodeURIComponent(char));
    })
    .join('/');
}
