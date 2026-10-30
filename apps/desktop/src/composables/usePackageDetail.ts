import { computed, inject, provide } from 'vue';
import type { ComputedRef, InjectionKey, Ref } from 'vue';
import { unwrap } from '@opennavo/api';
import type { PackageDetail, PublicComponents } from '@opennavo/api';
import { api } from '@/api';
import type { Kind } from '@/ipc/bindings';
import { commands, unwrap as unwrapIpc } from '@/ipc/client';
import type { LocalItem } from '@/ipc/client';
import { useLibraryStore } from '@/stores/library';
import { installedPackage, optionalPublicPackage } from '@/utils/publicCatalog';
import { useAppLocale } from './useAppLocale';
import { useLoader } from './useLoader';
import { useRemoteLoader } from './useRemoteLoader';
import { useCatalogLoader } from './useCatalogLoader';

type ReleaseStats = PublicComponents['schemas']['ReleaseStats'];

export interface PackageContext {
  kind: ComputedRef<Kind>;
  token: ComputedRef<string>;
  /** API details; undefined offline or on request failure. */
  detail: Ref<PackageDetail | undefined>;
  /** Local catalog entry, used for the header and summary offline. */
  local: Ref<LocalItem | null | undefined>;
  releaseStats: ComputedRef<ReleaseStats | null | undefined>;
  error: Ref<unknown>;
  reload: () => Promise<void>;
}

const key: InjectionKey<PackageContext> = Symbol('package');

/** The detail shell reads once; four tabs share data through inject (06 §13: API details + local entry). */
export function providePackage(props: { kind: Kind; token: string }): PackageContext {
  const { appLocale } = useAppLocale();
  const kind = computed(() => props.kind);
  const token = computed(() => props.token);
  const library = useLibraryStore();

  const {
    data: detail,
    error,
    reload
  } = useRemoteLoader(
    'package',
    () =>
      optionalPublicPackage(
        unwrap(
          api.GET('/packages/{kind}/{token}', {
            params: { path: { kind: kind.value, token: token.value } }
          })
        )
      ),
    [kind, token, appLocale]
  );

  const { data: catalog } = useCatalogLoader(
    async () => {
      const item = await unwrapIpc(commands.catalogGet(kind.value, token.value));
      return item;
    },
    [kind, token, appLocale],
    item => (item ? [item] : [])
  );
  const local = computed(() => {
    const installed = library.find(kind.value, token.value);
    return catalog.value ?? (installed ? installedPackage(installed) : null);
  });

  // Release cadence: use detail releaseStats; fall back to timeline statistics for older backends without this field.
  const { data: fallbackStats } = useLoader(async () => {
    if (!detail.value || detail.value.releaseStats) return null;
    const page = await optionalPublicPackage(
      unwrap(
        api.GET('/packages/{kind}/{token}/releases', {
          params: { path: { kind: kind.value, token: token.value }, query: { size: 1 } }
        })
      )
    );
    return page?.stats ?? null;
  }, [detail]);

  const releaseStats = computed(() => detail.value?.releaseStats ?? fallbackStats.value);
  const context: PackageContext = { kind, token, detail, local, releaseStats, error, reload };
  provide(key, context);
  return context;
}

export function usePackage(): PackageContext {
  const context = inject(key);
  if (!context) throw new Error('usePackage must be used within a detail tab');
  return context;
}
