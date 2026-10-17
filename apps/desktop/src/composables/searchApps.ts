import type { Page } from '@/ipc/bindings';
import { commands, unwrap } from '@/ipc/client';
import type { LocalSearchHit } from '@/ipc/client';

/**
 * Local catalog search over casks and link apps (store/security plan §4), never command-line tools (ADR-018).
 * includeLinks makes Rust rank and page both kinds together.
 */
export async function searchApps(q: string, limit: number): Promise<Page<LocalSearchHit>> {
  return unwrap(
    commands.catalogSearch({
      q,
      kind: 'cask',
      includeLinks: true,
      category: null,
      includeDisabled: false,
      limit,
      offset: 0
    })
  );
}
