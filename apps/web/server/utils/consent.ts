// Cloudflare's CF-IPCountry header decides the region. A missing header (no Cloudflare in front),
// "XX" (unknown) and "T1" (Tor) count as consent regions rather than being assumed exempt.
export function requiresConsent(country: string | undefined, regions: string[]): boolean {
  const code = (country ?? '').trim().toUpperCase();
  return !/^[A-Z]{2}$/.test(code) || code === 'XX' || code === 'T1' || regions.includes(code);
}
