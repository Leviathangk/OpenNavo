import type { AppError, UpdateConsent, UpdateWarning } from './bindings';

/** Current codes are specific. Interpret older persisted history by its exact reason. */
export function updateErrorKey(error: { code: string; message: string }): string {
  if (error.code !== 'E_UPDATE_INTEGRITY') return `errors.${error.code}`;
  if (['release_download_digest', 'download_length', 'download_sha256'].includes(error.message))
    return 'errors.E_UPDATE_DIGEST';
  if (['eddsa_key', 'eddsa_signature', 'eddsa_verification'].includes(error.message))
    return 'errors.E_UPDATE_FEED_SIGNATURE';
  if (['invalid_bundle_signature', 'pkg_signature_invalid', 'unsigned_signature_scope'].includes(error.message))
    return 'errors.E_UPDATE_SIGNATURE';
  return 'errors.E_UPDATE_CHANGED';
}
const warnings: readonly UpdateWarning[] = ['unsigned_developer', 'repair_installed', 'not_notarized', 'non_newer'];
export function updateChallenge(error: AppError | null | undefined): UpdateConsent | undefined {
  if (error?.code !== 'E_UPDATE_CONFIRMATION' || !error.detail) return undefined;
  try {
    const value = JSON.parse(error.detail) as UpdateConsent;
    if (
      !/^[a-f0-9]{64}$/.test(value.key) ||
      !Array.isArray(value.warnings) ||
      !value.warnings.length ||
      !value.warnings.every(w => warnings.includes(w))
    )
      return undefined;
    return { key: value.key, warnings: [...new Set(value.warnings)] };
  } catch {
    return undefined;
  }
}
