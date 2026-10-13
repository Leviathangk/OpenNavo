import { ApiError, unwrap } from '@opennavo/api';
import type { PublicComponents } from '@opennavo/api';
import { isValidToken } from '@opennavo/shared';
import type { PackageKind } from '@opennavo/shared';
import { packagePath } from '~/utils/packageView';

type RemovedPackage = PublicComponents['schemas']['RemovedPackage'];

/**
 * Detail data (05 §3): parent route and tabs share one key/request. When the detail is missing
 * (WP2) the lifecycle endpoint decides: a renamed app redirects permanently to its current token
 * with the same language and tab, a removed app renders a 410 summary, anything else is a 404. If
 * that check itself fails the page shows the retryable error state with a 503, so an outage is never
 * cached as 404 or 410.
 */
export async function usePackage(kind: PackageKind) {
  const { locale } = useI18n();
  const api = useApi();
  const route = useRoute();
  const localePath = useLocalePath();
  // Composables below run after awaits, where the Nuxt context is gone unless restored.
  const nuxtApp = useNuxtApp();
  const event = useRequestEvent();
  const token = computed(() => String(route.params.token ?? ''));
  const removed = ref<RemovedPackage | null>(null);

  if (!isValidToken(token.value))
    throw createError({ statusCode: 404, statusMessage: 'Package not found', fatal: true });

  const result = await useAsyncData(
    () => `package:${kind}:${token.value}:${locale.value}`,
    () =>
      unwrap(api.GET('/packages/{kind}/{token}', { params: { path: { kind, token: token.value } } })).catch(
        (error: unknown) => {
          // Convert business 404s into page 404s; other errors remain retryable page states.
          if (error instanceof ApiError && (error.isNotFound || error.status === 404))
            throw createError({ statusCode: 404, statusMessage: 'Package not found' });
          throw error;
        }
      )
  );

  if (result.error.value?.statusCode === 404) {
    const lifecycle = await nuxtApp.runWithContext(() =>
      useAsyncData(
        () => `package-lifecycle:${kind}:${token.value}:${locale.value}`,
        () =>
          unwrap(
            api.GET('/packages/{kind}/{token}/lifecycle', { params: { path: { kind, token: token.value } } })
          ).catch((error: unknown) => {
            if (error instanceof ApiError && (error.isNotFound || error.status === 404)) return null;
            throw error;
          })
      )
    );
    const value = lifecycle.data.value;
    // A failed check keeps the retryable error state; plugins/ssr-errors answers 503 (no-store).
    if (lifecycle.error.value) return { ...result, token, removed };
    if (value?.state === 'renamed') {
      const last = route.path.split('/').at(-1);
      const tab = last === 'versions' || last === 'dependencies' || last === 'details' ? last : undefined;
      await nuxtApp.runWithContext(() => navigateTo(localePath(packagePath(value.token, tab)), { redirectCode: 301 }));
    } else if (value?.state === 'removed' && value.removed) {
      removed.value = value.removed;
      if (event) setResponseStatus(event, 410);
    } else {
      throw createError({ statusCode: 404, statusMessage: 'Package not found', fatal: true });
    }
  }

  return { ...result, token, removed };
}
