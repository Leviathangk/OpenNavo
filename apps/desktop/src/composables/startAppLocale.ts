import { i18n } from '@/i18n';
import { commands, events, unwrap } from '@/ipc/client';
import type { Ref } from 'vue';
import type { DesktopLocale as Locale } from '@opennavo/shared';

/** Both windows read only the effective language; tray does not read full settings. */
export async function startAppLocale(current: Ref<Locale> = i18n.global.locale, root = document.documentElement) {
  const apply = (locale: Locale) => {
    current.value = locale;
    root.lang = locale;
  };
  apply(await unwrap(commands.localeGet()).catch(() => 'en-US' as const));
  let received = false;
  const stop = await events.localeChanged.listen(event => {
    received = true;
    apply(event.payload.locale);
  });
  // Language can change between initial read and subscription; reread without overwriting newer events.
  const latest = await unwrap(commands.localeGet()).catch(() => null);
  if (latest && !received) apply(latest);
  return stop;
}
