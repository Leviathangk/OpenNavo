import { buildDeepLink } from '@opennavo/shared';
import { trackEvent } from '~/utils/analytics';
import { launchDeepLink } from '~/utils/deepLink';
import type { PackageKind } from '@opennavo/shared';

export interface OpenInAppTarget {
  kind: PackageKind;
  token: string;
  name: string;
}

const SKIP_PROMPT_KEY = 'onv:skip-app-prompt';
// If the page has not blurred this long after a deep link, assume the client is absent (05 §7.1).
const DETECT_MS = 1500;

/**
 * Open in client via deep-link handoff; the client confirms action=install, while web only navigates.
 * Without client detection, show the layout's OpenInAppDialog asking whether OpenNavo is installed.
 * After Don't ask again, copy install commands directly instead.
 */
export function useOpenInApp() {
  const prompt = useState<OpenInAppTarget | null>('open-in-app:prompt', () => null);
  const { copy } = useCopyCommand();

  function open(target: OpenInAppTarget) {
    trackEvent('open_in_app');
    const url = buildDeepLink({ type: 'package', kind: target.kind, token: target.token, action: 'install' });
    let left = false;
    const mark = () => {
      left = true;
    };
    window.addEventListener('blur', mark, { once: true });
    document.addEventListener('visibilitychange', mark, { once: true });
    launchDeepLink(url);
    setTimeout(() => {
      window.removeEventListener('blur', mark);
      document.removeEventListener('visibilitychange', mark);
      if (left) return;
      if (localStorage.getItem(SKIP_PROMPT_KEY) === '1') void copy(target.kind, target.token);
      else prompt.value = target;
    }, DETECT_MS);
  }

  function skipPrompt() {
    localStorage.setItem(SKIP_PROMPT_KEY, '1');
  }

  return { open, prompt, skipPrompt };
}
