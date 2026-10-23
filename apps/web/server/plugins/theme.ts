// Both themes (08 §3.7): every page is served dark, the site's default, and the head script in nuxt.config
// applies a saved choice before first paint. Pages are cached and shared, so the HTML never carries the choice.
// Only the server writes the attribute: Nuxt pushes app.head on the client too, so in app.head.htmlAttrs the
// client's head renderer would set it back to dark when hydration ends, over the saved choice.
export default defineNitroPlugin(nitroApp => {
  nitroApp.hooks.hook('render:html', html => {
    html.htmlAttrs.push('data-theme="dark"');
  });
});
