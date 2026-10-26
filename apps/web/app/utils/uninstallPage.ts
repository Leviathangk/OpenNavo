import { robotsAllowsIndexing } from './indexing';
// "How to uninstall {app} on Mac" intent page (X2), pure for testing. Like the alternatives page it exists
// only where the server's decision opens it (seo pages.uninstall: indexable with its own canonical, after
// intentPagesEnabled, a confirmed search target and a concrete upstream uninstall declaration), so each
// app × intent × language has one target page; anywhere else it is a 404 and the app page keeps its
// Uninstall section.
import type { PublicComponents } from '@opennavo/api';
import { BRAND } from '@opennavo/shared';
import { uninstallLink } from './backgroundItems';

type PackageDetail = PublicComponents['schemas']['PackageDetail'];

/** Whether the server opens the uninstall page for this app in this language. */
export function uninstallPageOpen(pkg: Pick<PackageDetail, 'kind' | 'facts' | 'indexing'>): boolean {
  const decision = pkg.indexing?.pages?.uninstall;
  return Boolean(
    pkg.kind === 'cask' &&
    decision &&
    robotsAllowsIndexing(decision.robots) &&
    !decision.canonicalPath &&
    pkg.facts?.uninstall?.length
  );
}

/** The client's uninstall preview for this app (it lists what it finds and deletes nothing on its own). */
export function uninstallPreviewLink(token: string): string | null {
  return uninstallLink({ uninstallUrl: `${BRAND.scheme}://uninstall/${token}?preview=1` });
}
