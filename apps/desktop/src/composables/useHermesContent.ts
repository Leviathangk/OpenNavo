import { computed } from 'vue';
import { useI18n } from 'vue-i18n';
import { en, zhCN } from '@/hermes/content';

/** Hermes copy is scoped to the assistant; zh-CN is complete, other locales use English until translated. */
export function useHermesContent() {
  const { locale } = useI18n();
  return computed(() => (locale.value === 'zh-CN' ? zhCN : en));
}
