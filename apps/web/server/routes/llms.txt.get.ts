import { defineEventHandler, setHeaders, setResponseStatus } from 'h3';
import { renderLlms } from '../utils/llms';
import { siteMessage } from '../utils/siteCopy';

export default defineEventHandler(event => {
  const config = useRuntimeConfig(event);
  // No shared cache: switching off must not leave an old 200 document available at the edge.
  setHeaders(event, {
    'Content-Type': 'text/plain; charset=utf-8',
    'Cache-Control': 'no-store',
    'X-Robots-Tag': 'noindex, follow'
  });
  const enabled: unknown = config.llmsEnabled;
  if (enabled !== true && enabled !== 'true') {
    setResponseStatus(event, 404);
    return siteMessage('en-US', 'error.notFoundTitle');
  }
  return renderLlms(config.public.siteUrl);
});
