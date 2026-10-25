import { toValue, watch, type MaybeRefOrGetter } from 'vue';
import type { PublicComponents } from '@opennavo/api';
import { brandDownloadPath } from '~/utils/packageView';
import { hreflangsFor } from '~~/shared/utils/hreflang';

type PageDecision = PublicComponents['schemas']['PageDecision'];

/**
 * Applies the server's decision for this URL (S5): robots (meta, and the X-Robots-Tag header through
 * plugins/robots-header.server.ts), the canonical path and the hreflang alternates app.vue writes. The web
 * never re-derives indexability. The state is set during setup, after the decision's data has loaded, so
 * SSR renders it; `reset` clears it when the owner unmounts (the versions tab hands back to its layout).
 * A canonical naming OpenNavo's own overview names that language's /download, where the overview's 301 goes.
 */
export function usePageDecision(
  decision: MaybeRefOrGetter<PageDecision | null | undefined>,
  { reset = true }: { reset?: boolean } = {}
) {
  const hreflangs = useState<string[] | null>('page:hreflangs', () => null);
  const canonicalPath = useState<string | null>('page:canonicalPath', () => null);
  useSeoMeta({ robots: () => toValue(decision)?.robots });
  let ownedHreflangs: string[] | null = null;
  const canonicalOwner = useState<number>('page:canonicalOwner', () => 0);
  let ownedCanonical = -1;
  const apply = (value: PageDecision | null | undefined) => {
    if (!value) return;
    hreflangs.value = hreflangsFor(value.hreflang);
    ownedHreflangs = hreflangs.value;
    canonicalPath.value = value.canonicalPath ? (brandDownloadPath(value.canonicalPath) ?? value.canonicalPath) : null;
    ownedCanonical = ++canonicalOwner.value;
  };
  apply(toValue(decision));
  watch(() => toValue(decision), apply);
  if (reset)
    onUnmounted(() => {
      // A new page can finish setup before the old page unmounts.
      if (hreflangs.value === ownedHreflangs) hreflangs.value = null;
      if (canonicalOwner.value === ownedCanonical) canonicalPath.value = null;
    });
}
