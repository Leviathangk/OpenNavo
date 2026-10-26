// Apply on every response, including Nitro SWR cache hits. HTML only: assets and
// API clients do not need a browsing-context policy. Keep OAuth/payment popups usable.
export default defineNitroPlugin(nitroApp => {
  nitroApp.hooks.hook('beforeResponse', event => {
    const contentType = String(event.node.res.getHeader('content-type') ?? '');
    if (contentType.includes('text/html')) {
      setHeader(event, 'Cross-Origin-Opener-Policy', 'same-origin-allow-popups');
    }
  });
});
