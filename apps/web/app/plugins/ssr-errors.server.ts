// useAsyncData captures failures instead of throwing. Do not cache the rendered error UI as a
// successful page: a captured failure is essential data, so the page answers 503 (no-store through
// server/plugins/error-cache-control.ts). Decorative sections catch their own failures
// (utils/decorative.ts, S7) and so never reach this list.
export default defineNuxtPlugin(nuxtApp => {
  const event = useRequestEvent();
  nuxtApp.hook('app:rendered', () => {
    // Nuxt's serialized payload uses this field for captured useAsyncData failures.
    const { _errors: errors } = nuxtApp.payload;
    if (event && event.node.res.statusCode < 400 && Object.values(errors).some(Boolean)) {
      setResponseStatus(event, 503);
    }
  });
});
