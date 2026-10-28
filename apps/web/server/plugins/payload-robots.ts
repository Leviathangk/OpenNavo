// Nitro may serve a cached/extracted payload without running Nuxt's head plugin.
// Remember the original request before Nuxt rewrites its URL, then override the final header.
export default defineNitroPlugin(nitro => {
  nitro.hooks.hook('request', event => {
    event.context.noindexPayload = event.path.split('?', 1)[0]?.endsWith('/_payload.json') === true;
  });
  nitro.hooks.hook('beforeResponse', event => {
    if (event.context.noindexPayload) setHeader(event, 'X-Robots-Tag', 'noindex');
  });
});
