import { installCommand } from '@opennavo/shared';
import type { PackageKind } from '@opennavo/shared';
import { trackEvent } from '~/utils/analytics';

/** Copy install command (05 §7.3): show it on success; without clipboard permission, include it in a notice for manual copying. */
export function useCopyCommand() {
  const { t } = useI18n();
  const toasts = useToasts();

  async function copy(kind: PackageKind, token: string, command = installCommand(kind, token)) {
    try {
      await navigator.clipboard.writeText(command);
      toasts.push({ tone: 'success', title: t('toast.copied'), description: command });
      trackEvent('copy_install_command');
    } catch {
      toasts.push({ tone: 'warning', title: t('toast.copyFailed'), description: command, duration: 0 });
    }
  }

  return { copy };
}
