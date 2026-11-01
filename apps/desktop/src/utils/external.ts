import { isTauri } from '@tauri-apps/api/core';

// The opener capability also allows x-apple.systempreferences:, which only explicit in-app calls
// (security settings buttons) may use. Everything else, including delegated anchor clicks, must be
// a public http(s) URL: a page cannot send the system browser to loopback or this machine's LAN.
const SYSTEM_SETTINGS_PREFIX = 'x-apple.systempreferences:';

function isPrivateIpv4Bytes(a: number, b: number): boolean {
  return (
    a === 0 ||
    a === 10 ||
    a === 127 ||
    (a === 169 && b === 254) ||
    (a === 172 && b >= 16 && b <= 31) ||
    (a === 192 && b === 168)
  );
}

function isPrivateIpv4(host: string): boolean {
  const numbers = host.split('.').map(Number);
  if (numbers.length !== 4 || numbers.some(value => !Number.isInteger(value) || value < 0 || value > 255)) {
    return false;
  }
  return isPrivateIpv4Bytes(numbers[0]!, numbers[1]!);
}

function ipv4Number(a: number, b: number): number {
  return a * 256 + b;
}

function ipv6Groups(hostname: string): number[] | null {
  const host = hostname.toLowerCase().split('%')[0] ?? '';
  if (!host || !host.includes(':')) return null;
  const parts = host.split('::');
  if (parts.length > 2) return null;
  const words = (part: string | undefined): number[] | null => {
    if (!part) return [];
    const groups: number[] = [];
    for (const segment of part.split(':')) {
      if (segment.includes('.')) {
        const dotted = segment.split('.').map(Number);
        if (dotted.length !== 4 || dotted.some(value => !Number.isInteger(value) || value < 0 || value > 255)) {
          return null;
        }
        groups.push(ipv4Number(dotted[0]!, dotted[1]!), ipv4Number(dotted[2]!, dotted[3]!));
      } else {
        if (!/^[0-9a-f]{1,4}$/.test(segment)) return null;
        groups.push(Number.parseInt(segment, 16));
      }
    }
    return groups;
  };
  const head = words(parts[0]);
  const tail = parts.length === 2 ? words(parts[1]) : [];
  if (!head || !tail) return null;
  if (parts.length === 2) {
    const missing = 8 - head.length - tail.length;
    if (missing < 0) return null;
    return [...head, ...Array<number>(missing).fill(0), ...tail];
  }
  return head.length === 8 ? head : null;
}

function isPrivateIpv6Bytes(groups: number[]): boolean {
  if (groups.length !== 8) return true;
  const [g0, g1, g2, g3, g4, g5, g6] = groups as [number, number, number, number, number, number, number];
  const embedded = (high: number) => isPrivateIpv4Bytes(high >> 8, high & 0xff);
  const compatible = g0 === 0 && g1 === 0 && g2 === 0 && g3 === 0 && g4 === 0;
  // IPv4-mapped (::ffff:a.b.c.d) and IPv4-compatible (::a.b.c.d, including :: and ::1).
  if ((compatible && g5 === 0xffff) || (compatible && g5 === 0)) return embedded(g6);
  if ((g0 & 0xfe00) === 0xfc00) return true; // fc00::/7 unique local
  if ((g0 & 0xffc0) === 0xfe80) return true; // fe80::/10 link-local
  return false;
}

function isPrivateHost(host: string): boolean {
  if (host === 'localhost' || host.endsWith('.localhost')) return true;
  if (host.startsWith('[') && host.endsWith(']')) {
    const groups = ipv6Groups(host.slice(1, -1));
    return groups ? isPrivateIpv6Bytes(groups) : false;
  }
  return isPrivateIpv4(host);
}

/** A public http(s) URL: no credentials and a host outside loopback, LAN, link-local and IPv6 ULA ranges. */
export function isPublicHttpUrl(value: string): boolean {
  let url: URL;
  try {
    url = new URL(value);
  } catch {
    return false;
  }
  if (url.protocol !== 'http:' && url.protocol !== 'https:') return false;
  if (url.username || url.password) return false;
  const host = url.hostname.toLowerCase().replace(/\.$/, '');
  return Boolean(host) && !isPrivateHost(host);
}

/** Whether openExternal may hand this URL to the OS: a public web URL, or an explicit system-settings link. */
export function isAllowedExternalUrl(value: string): boolean {
  const url = value.trim();
  if (url.startsWith(SYSTEM_SETTINGS_PREFIX)) return url.length > SYSTEM_SETTINGS_PREFIX.length;
  return isPublicHttpUrl(url);
}

/** Open external links in the system browser (opener permits http:, https: and x-apple.systempreferences:, 06 §14); browser mode opens a new tab. */
export async function openExternal(url: string): Promise<void> {
  if (!isAllowedExternalUrl(url)) throw new Error('external_url_rejected');
  if (isTauri()) {
    const { openUrl } = await import('@tauri-apps/plugin-opener');
    await openUrl(url);
    return;
  }
  window.open(url, '_blank', 'noopener,noreferrer');
}

/** Shared UI anchors keep normal browser behavior; the desktop routes their clicks through its scoped opener. */
export function startExternalLinks(): () => void {
  if (!isTauri()) return () => {};
  const onClick = (event: MouseEvent) => {
    if (event.defaultPrevented || event.button !== (event.type === 'auxclick' ? 1 : 0)) return;
    const link = event.composedPath().find(node => node instanceof HTMLAnchorElement);
    if (!(link instanceof HTMLAnchorElement) || link.target.toLowerCase() !== '_blank') return;
    if (!isPublicHttpUrl(link.href)) return;
    event.preventDefault();
    void openExternal(link.href);
  };
  document.addEventListener('click', onClick);
  document.addEventListener('auxclick', onClick);
  return () => {
    document.removeEventListener('click', onClick);
    document.removeEventListener('auxclick', onClick);
  };
}
