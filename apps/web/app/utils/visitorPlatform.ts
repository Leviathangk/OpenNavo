// What the page can tell about the visitor's computer, to list the downloads that suit it. Pure, for testing; nothing here
// is sent anywhere: the detection only decides which of the files on the page to show first.
import type { MacChip } from '@opennavo/shared';

export type VisitorSystem = 'mac' | 'windows' | 'linux' | 'ios' | 'android' | 'other';

/**
 * The system, from the client hints' platform name and the user-agent text. A "Macintosh" with a touch screen is an iPad
 * asking for the desktop site.
 */
export function visitorSystem(userAgent: string, platformHint: string | undefined, touchPoints: number): VisitorSystem {
  const text = `${platformHint ?? ''} ${userAgent}`;
  if (/iPhone|iPad|iPod/i.test(text) || (/Mac/i.test(text) && touchPoints > 1)) return 'ios';
  if (/Android/i.test(text)) return 'android';
  if (/Mac/i.test(text)) return 'mac';
  if (/Win/i.test(text)) return 'windows';
  if (/Linux|X11|CrOS/i.test(text)) return 'linux';
  return 'other';
}

/**
 * The chip of a Mac from the name of its graphics hardware, where the browser gives it. Chrome and Firefox on Apple
 * silicon name the chip ("Apple M2 Pro"), which holds under Rosetta too, while the user-agent text says "Intel" there.
 * WebKit says only "Apple GPU" on every Mac, Intel ones included, so a bare "Apple" is no answer: Safari stays unknown.
 */
export function chipFromRenderer(renderer: string | null | undefined): MacChip | null {
  if (!renderer) return null;
  if (/\bApple M\d/i.test(renderer)) return 'arm64';
  if (/Intel|AMD|Radeon|NVIDIA|GeForce/i.test(renderer)) return 'x86_64';
  return null;
}

/** The chip from the client hints' architecture, for browsers that hide the graphics hardware's name. */
export function chipFromArchitecture(architecture: string | undefined): MacChip | null {
  if (architecture === 'arm') return 'arm64';
  if (architecture === 'x86') return 'x86_64';
  return null;
}
