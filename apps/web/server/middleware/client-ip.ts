import { clientIP } from '../utils/client-ip';
import { captureCrawler } from '../utils/crawlerMetrics';

// Save identity before Nitro SWR wraps requests/filters headers; page caches remain shared.
export default defineEventHandler(event => {
  const config = useRuntimeConfig(event);
  captureCrawler(event, config);
  event.context.publicApiClientIP = clientIP(
    event.node.req.socket.remoteAddress,
    getHeader(event, 'x-forwarded-for'),
    config.trustedProxies
  );
});
