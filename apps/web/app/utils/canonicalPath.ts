/**
 * The path as canonicals, hreflang and sitemaps spell it: each segment decoded, then written with encodeURI,
 * so app tokens keep their literal + and @ (plus-app++, not plus-app%2B%2B, which the server moves back with a
 * 301) while spaces and other characters stay escaped. A slash, ? or # inside a segment stays escaped too.
 */
export function canonicalPath(path: string): string {
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
