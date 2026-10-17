import type { DownloadVerdict } from '@/ipc/bindings';

export type VerdictTone = 'success' | 'warning' | 'danger';

type Translate = (key: string, named?: Record<string, unknown>) => string;

/** File name of a download path, for notices. */
export const fileName = (path: string) => path.split('/').filter(Boolean).at(-1) ?? path;

/**
 * What a download's verdict means (store/security plan §4, "downloads you can trust"): danger for a threat, an
 * invalid signature or another developer than the one registered; warning when signature or notarization is missing;
 * success when everything checked agrees. The parts read in the mockup's order: signature, notarization, scan.
 */
export function describeVerdict(verdict: DownloadVerdict, t: Translate): { tone: VerdictTone; parts: string[] } {
  const { signature, av } = verdict;
  const parts: string[] = [];
  let tone: VerdictTone = 'success';
  const raise = (next: VerdictTone) => {
    if (next === 'danger' || (next === 'warning' && tone === 'success')) tone = next;
  };

  const signer = signature.signer ?? signature.teamId ?? '';
  const who =
    signature.teamId && signature.signer ? t('package.link.verdict.who', { signer, team: signature.teamId }) : signer;
  if (signature.status === 'invalid') {
    parts.push(t('package.link.verdict.invalid'));
    raise('danger');
  } else if (signature.status === 'modified') {
    // A fresh download should match its seal; Rust refuses to open it, as for an invalid signature.
    parts.push(t('package.link.verdict.modified'));
    raise('danger');
  } else if (verdict.format === 'zip' && (signature.status === 'not_code' || signature.status === 'unsigned')) {
    // ZIPs are not unpacked here, so the app inside is checked by macOS when it is first opened.
    parts.push(t('package.link.verdict.zip'));
  } else if (signature.status === 'not_code') {
    parts.push(t('security.signature.notCode'));
  } else if (signature.status === 'unknown') {
    parts.push(t('security.signature.unknown'));
    raise('warning');
  } else if (signature.status === 'unsigned' || signature.status === 'adhoc') {
    parts.push(t('package.link.verdict.unsigned'));
    raise('warning');
  } else if (verdict.teamMatches === false) {
    parts.push(t('package.link.verdict.teamMismatch', { who }));
    raise('danger');
  } else if (verdict.teamMatches) parts.push(t('package.link.verdict.teamMatch', { who }));
  else parts.push(t('package.link.verdict.signed', { who }));

  if (signature.notarized === true) parts.push(t('package.link.verdict.notarized'));
  else if (signature.notarized === false) {
    parts.push(t('package.link.verdict.notNotarized'));
    raise('warning');
  }

  // Why there is no scan result: no database, no consent, an excluded folder, a file over 100 MiB, or unreadable.
  if (!av)
    parts.push(
      verdict.avSkipped
        ? t('package.link.verdict.skipped', { reason: t(`security.avSkipped.${verdict.avSkipped}`) })
        : t('package.link.verdict.notScanned')
    );
  else if (av.clean) parts.push(t('package.link.verdict.clean'));
  else {
    const name = av.signature ?? '';
    parts.push(
      t(verdict.quarantined ? 'package.link.verdict.threatQuarantined' : 'package.link.verdict.threat', { name })
    );
    raise('danger');
  }
  return { tone, parts };
}
