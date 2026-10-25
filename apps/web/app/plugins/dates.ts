import type { Ref } from 'vue';
import { setDateDefaults } from '@opennavo/shared';

// Pages are served from caches for hours and hydrated in each visitor's time zone, so the server and the
// browser must format every date alike (the desktop app sets nothing and keeps the Mac's local time):
// dates use UTC, and clock times say so (14:05 UTC). "Now", for relative times and this-year checks, is
// the instant the server rendered the page, which the payload carries to the browser. Once the page is
// ready the visitor's clock takes over and relative times catch up with the age of the cached copy.
const renderedAt = new WeakMap<object, Ref<number | null>>();

export default defineNuxtPlugin(nuxtApp => {
  const instant = useState<number | null>('onv:rendered-at', () => Date.now());
  renderedAt.set(nuxtApp, instant);
  setDateDefaults({ timeZone: 'UTC', now: renderNow });
  // After hydration and idle, so the one re-render it causes never lengthens hydration itself.
  onNuxtReady(() => {
    instant.value = null;
  });
});

// The server renders several requests at a time, so the instant comes from the app being rendered.
// Outside an app, or once the page is ready, the clock answers.
function renderNow(): Date {
  const app = tryUseNuxtApp();
  const at = app ? renderedAt.get(app)?.value : null;
  return typeof at === 'number' ? new Date(at) : new Date();
}
