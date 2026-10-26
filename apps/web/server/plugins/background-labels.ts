import { labelPath } from '../../shared/utils/backgroundLabels';
import { backgroundItemRenderUrl, backgroundItemRoute } from '../utils/canonicalUrl';

// Run after the SWR wrapper restores the raw URL and before Nuxt encodes event.path. Keep the
// request URL/cache key encoded; only the renderer receives the once-decoded, validated label.
export default defineNitroPlugin(nitroApp => {
  nitroApp.hooks.hook('render:before', ({ event }) => {
    const url = event.node.req.url ?? event.path;
    const route = backgroundItemRoute(url);
    if (!route) return;
    if (route.label === undefined) throw createError({ statusCode: 404, statusMessage: 'Page not found' });
    event.context.backgroundLabelPath = labelPath(route.label, route.prefix);
    event['_path'] = backgroundItemRenderUrl(url);
  });
});
