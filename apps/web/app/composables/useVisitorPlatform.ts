import type { MacChip } from '@opennavo/shared';
import { chipFromArchitecture, chipFromRenderer, visitorSystem } from '~/utils/visitorPlatform';
import type { VisitorSystem } from '~/utils/visitorPlatform';

interface ClientHints {
  platform?: string;
  getHighEntropyValues?: (hints: string[]) => Promise<{ architecture?: string }>;
}

/** The name of the graphics hardware, where the browser shows it. */
function graphicsRenderer(): string | null {
  try {
    const gl = document.createElement('canvas').getContext('webgl');
    if (!gl) return null;
    const info = gl.getExtension('WEBGL_debug_renderer_info');
    const renderer = info ? String(gl.getParameter(info.UNMASKED_RENDERER_WEBGL)) : null;
    gl.getExtension('WEBGL_lose_context')?.loseContext();
    return renderer;
  } catch {
    return null;
  }
}

/**
 * The visitor's system and, on a Mac, its chip. Null on the server and until the page is mounted, so the first render
 * is the same on both and lists everything; the chip stays null when the browser does not say.
 */
export function useVisitorPlatform() {
  const platform = ref<{ system: VisitorSystem; chip: MacChip | null } | null>(null);
  onMounted(async () => {
    const hints = (navigator as Navigator & { userAgentData?: ClientHints }).userAgentData;
    const system = visitorSystem(navigator.userAgent, hints?.platform, navigator.maxTouchPoints);
    let chip: MacChip | null = null;
    if (system === 'mac') {
      chip = chipFromRenderer(graphicsRenderer());
      if (!chip) {
        const values = await hints?.getHighEntropyValues?.(['architecture']).catch(() => undefined);
        chip = chipFromArchitecture(values?.architecture);
      }
    }
    platform.value = { system, chip };
  });
  return platform;
}
