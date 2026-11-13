import { pluralRules } from '@opennavo/shared';

export default defineI18nConfig(() => ({
  // Untranslated developer copy falls back to English instead of rendering raw keys.
  fallbackLocale: 'en',
  pluralRules
}));
