// Nuxt's encodePath also collapses %252F to %2F. A valid label containing the literal text
// "%2F" must retain its identity. Restore the server-validated encoded path before nuxt:router
// parses it; the renderer has already removed the payload suffix and its build query by now.
export default defineNuxtPlugin({
  name: 'background-label-url',
  order: -30,
  setup({ ssrContext }) {
    const path = ssrContext?.event.context.backgroundLabelPath;
    if (!ssrContext || typeof path !== 'string') return;
    const queryAt = ssrContext.url.indexOf('?');
    ssrContext.url = path + (queryAt < 0 ? '' : ssrContext.url.slice(queryAt));
  }
});
