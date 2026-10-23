import { computed } from 'vue';
import { useI18n } from 'vue-i18n';
import { pick as pickLocalizedText } from '@opennavo/shared';
import type { DesktopLocale as Locale } from '@opennavo/shared';
import type { LocalizedText } from '@/ipc/bindings';

/** Common fallback for the current UI locale and sparse catalog text. */
export function useAppLocale() {
  const { locale } = useI18n();
  const appLocale = computed(() => locale.value as Locale);

  function pick(text: LocalizedText | null | undefined, fallback = '', sourceLocale: Locale = 'en-US'): string {
    return pickLocalizedText(text, appLocale.value, sourceLocale, fallback);
  }

  return { appLocale, pick };
}
