import { ref } from 'vue';
import { useI18n } from 'vue-i18n';
import { formatRelativeTime } from '@opennavo/shared';
import { useAppLocale } from '@/composables/useAppLocale';
import { usePackageState } from '@/composables/usePackageState';
import { useToasts } from '@/composables/useToasts';
import { useSecurityStore } from '@/stores/security';
import { openExternal } from '@/utils/external';

/**
 * VirusTotal lookups (store/security plan §6, S2, optional per D8): with the switch on and the user's own key saved,
 * a file's SHA-256 — never the file — goes to VirusTotal when the user asks. The verdict shows as a notice that
 * links to the full report.
 */
export function useVirusTotal() {
  const { t } = useI18n();
  const { appLocale } = useAppLocale();
  const toasts = useToasts();
  const { errorText } = usePackageState();
  const security = useSecurityStore();
  /** The SHA-256 being looked up. */
  const busy = ref<string | null>(null);

  // Whether a key is saved is known only after asking Rust once.
  if (security.securitySettings.virusTotalEnabled && !security.virusTotal)
    void security.loadVirusTotal().catch(() => undefined);

  async function lookup(sha256: string) {
    if (busy.value) return;
    busy.value = sha256;
    try {
      const verdict = await security.lookupVirusTotal(sha256);
      if (!verdict.found) {
        toasts.push({
          tone: 'info',
          title: t('security.virusTotal.notFound'),
          description: t('security.virusTotal.hashOnly')
        });
        return;
      }
      const { malicious, suspicious } = verdict;
      const total = malicious + suspicious + verdict.harmless + verdict.undetected;
      const title = malicious
        ? t('security.virusTotal.malicious', { count: malicious }, { plural: malicious })
        : suspicious
          ? t('security.virusTotal.suspicious', { count: suspicious }, { plural: suspicious })
          : t('security.virusTotal.clean');
      const analyzed = verdict.lastAnalysisAt
        ? t('security.virusTotal.analyzed', {
            time: formatRelativeTime(verdict.lastAnalysisAt, { locale: appLocale.value })
          })
        : '';
      toasts.pushAction(
        {
          tone: malicious ? 'danger' : suspicious ? 'warning' : 'success',
          title,
          description: [t('security.virusTotal.engines', { total }), analyzed].filter(Boolean).join(' · '),
          actionLabel: t('security.virusTotal.report')
        },
        () => void openExternal(verdict.permalink)
      );
    } catch (error) {
      toasts.push({ tone: 'danger', title: t('errors.actionFailed'), description: errorText(error) });
    } finally {
      busy.value = null;
    }
  }

  return { busy, lookup };
}
