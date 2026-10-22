import { defineEventHandler } from 'h3';
import { captureCrawler } from '../utils/crawlerMetrics';
import { loadShedConfig, shedRequest } from '../utils/loadShed';

// Before settings/API fetches, canonical middleware and the cached SSR route handler.
export default defineEventHandler(event => {
  const config = useRuntimeConfig(event);
  const response = shedRequest(event, loadShedConfig(config.loadShed));
  // Rejections bypass client-ip.ts; preserve the same trusted-proxy verification for their metrics.
  if (event.context.loadShed) captureCrawler(event, config);
  return response;
});
