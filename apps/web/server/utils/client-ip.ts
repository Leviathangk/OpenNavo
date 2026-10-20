import { isIP } from 'node:net';

/** Only the socket peer can establish the proxy hop; forwarded headers never establish trust. */
export function isTrustedProxy(peer: string | undefined, trustedProxies: string): boolean {
  const address = peer?.replace(/^::ffff:/, '');
  return Boolean(address && isIP(address) && trustedProxies.split(',').some(value => value.trim() === address));
}

// Accept forwarded addresses only from explicitly trusted connections; use the rightmost value to ignore forged client prefixes.
export function clientIP(
  peer: string | undefined,
  forwarded: string | undefined,
  trustedProxies: string
): string | undefined {
  const address = peer?.replace(/^::ffff:/, '');
  if (!address || !isIP(address)) return undefined;
  if (!isTrustedProxy(peer, trustedProxies)) return address;
  const forwardedIP = forwarded?.split(',').at(-1)?.trim();
  return forwardedIP && isIP(forwardedIP) ? forwardedIP : address;
}
