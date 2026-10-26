// Nuxt 4.5/Vite 8 currently emits an empty SSR styles map for this app. Inline the
// single combined stylesheet instead. It includes client-only/route styles too,
// so navigating never needs to load (or inject a second copy of) the stylesheet.
export default defineNitroPlugin(async nitroApp => {
  if (import.meta.dev) return;
  const storage = useStorage('assets:site-css');
  const styles = new Map<string, string>();
  for (const key of await storage.getKeys()) {
    if (!key.endsWith('.css')) continue;
    const css = await storage.getItem<string>(key);
    if (typeof css === 'string') styles.set(key, css);
  }

  nitroApp.hooks.hook('render:html', html => {
    const inlined = new Set<string>();
    html.head = html.head.map(part =>
      part.replace(/<link\b[^>]*>/g, link => {
        if (!/\brel="stylesheet"/.test(link)) return link;
        const href = /\bhref="([^"]+)"/.exec(link)?.[1];
        const name = href?.split('/').at(-1);
        const css = name && styles.get(name);
        if (!name || !css) return link;
        if (inlined.has(name)) return '';
        inlined.add(name);
        return `<style data-site-css="${name}">${css.replace(/<\/style/gi, '<\\/style')}</style>`;
      })
    );
  });
});
