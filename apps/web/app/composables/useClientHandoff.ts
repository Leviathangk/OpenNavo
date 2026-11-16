import { getCurrentScope, onScopeDispose } from 'vue';
import { buildDeepLink } from '@opennavo/shared';
import type { ClientRoute, PackageKind } from '@opennavo/shared';
import { trackEvent } from '~/utils/analytics';
import { launchDeepLink } from '~/utils/deepLink';

export type ClientHandoffKind = 'install' | 'collection' | 'scan' | 'uninstall' | 'route';

export interface ClientHandoffCopy {
  kind: PackageKind;
  token: string;
  command?: string;
}

export interface ClientHandoffTarget {
  /** The deep link to try. Navigation only: every write stays behind the client's own confirmation. */
  url: string;
  kind: ClientHandoffKind;
  /** Package, collection or page name shown in the dialog. */
  name?: string;
  /** Install command offered when the client is absent. */
  copy?: ClientHandoffCopy;
  /** Analytics label, e.g. install, scan, monitor. */
  feature?: string;
}

const PROMPT_KEY = 'client-handoff:prompt';
const OPENING_KEY = 'client-handoff:opening';
const SKIP_PROMPT_KEY = 'onv:skip-app-prompt';
// Opening the client can take a moment, especially on first launch. Give it long enough that a slow
// launch does not show the fallback over an app that is already opening.
const DETECT_MS = 2200;

/**
 * One handoff path for every web action that runs in the Mac app. The client only navigates or asks its
 * own confirmation; the web never runs brew, scans, cleanup or uninstall. If the page keeps focus after
 * the attempt, the shared dialog offers the download, a retry and — for installs — the brew command.
 */
export function useClientHandoff() {
  const prompt = useState<ClientHandoffTarget | null>(PROMPT_KEY, () => null);
  const opening = useState<boolean>(OPENING_KEY, () => false);
  const { copy } = useCopyCommand();
  // A page change unmounts the component that started the handoff. Cancel its pending detection so a
  // fallback dialog cannot appear on the next page, and release the shared opening flag with it.
  let cancelPending: (() => void) | null = null;

  if (getCurrentScope()) {
    onScopeDispose(() => {
      if (!cancelPending) return;
      cancelPending();
      cancelPending = null;
      opening.value = false;
    });
  }

  function open(target: ClientHandoffTarget) {
    if (opening.value) return;
    trackEvent('open_in_app', { feature: target.feature ?? target.kind });
    opening.value = true;
    let left = false;
    const mark = () => {
      left = true;
    };
    const markHidden = () => {
      if (document.visibilityState === 'hidden') left = true;
    };
    window.addEventListener('blur', mark, { once: true });
    document.addEventListener('visibilitychange', markHidden);
    const detach = () => {
      window.removeEventListener('blur', mark);
      document.removeEventListener('visibilitychange', markHidden);
    };
    launchDeepLink(target.url);
    const timer = setTimeout(() => {
      detach();
      cancelPending = null;
      opening.value = false;
      if (left) return;
      let skip = false;
      try {
        skip = localStorage.getItem(SKIP_PROMPT_KEY) === '1';
      } catch {
        skip = false;
      }
      if (skip && target.copy) {
        void copy(target.copy.kind, target.copy.token, target.copy.command);
        return;
      }
      prompt.value = target;
    }, DETECT_MS);
    cancelPending = () => {
      clearTimeout(timer);
      detach();
    };
  }

  function openPackage(target: { kind: PackageKind; token: string; name: string; command?: string }) {
    open({
      url: buildDeepLink({ type: 'package', kind: target.kind, token: target.token, action: 'install' }),
      kind: 'install',
      name: target.name,
      feature: 'install',
      copy: { kind: target.kind, token: target.token, command: target.command }
    });
  }

  function openCollection(slug: string, title: string) {
    open({
      url: buildDeepLink({ type: 'collection', slug, action: 'install' }),
      kind: 'collection',
      name: title,
      feature: 'collection'
    });
  }

  function openRoute(route: ClientRoute, feature?: string) {
    open({ url: buildDeepLink({ type: 'route', route }), kind: 'route', feature });
  }

  function openUrl(url: string, kind: Exclude<ClientHandoffKind, 'install' | 'collection' | 'route'>, name?: string) {
    open({ url, kind, name, feature: kind });
  }

  function close() {
    prompt.value = null;
  }

  function copyInstead() {
    const target = prompt.value;
    prompt.value = null;
    if (target?.copy) void copy(target.copy.kind, target.copy.token, target.copy.command);
  }

  function dontAsk() {
    try {
      localStorage.setItem(SKIP_PROMPT_KEY, '1');
    } catch {
      // Storage is optional; the dialog still closes.
    }
    copyInstead();
  }

  function retry() {
    const target = prompt.value;
    prompt.value = null;
    if (target) open(target);
  }

  return {
    prompt,
    opening,
    open,
    openPackage,
    openCollection,
    openRoute,
    openUrl,
    close,
    copyInstead,
    dontAsk,
    retry
  };
}
