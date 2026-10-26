/** Accept Nitro's parsed boolean and a raw environment string; default to the experiment being off. */
export function sitemapFontsEnabled(value: unknown): boolean {
  return value === true || value === 'true';
}
