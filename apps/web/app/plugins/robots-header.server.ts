// The robots module sets a route-level header before page data is loaded. Page-authored
// rules can override its meta tag, so copy the final, deduplicated directive to the header
// before Nitro stores the HTML and headers together in its SWR cache.
export default defineNuxtPlugin(() => {
  const payload = useRequestURL().pathname.endsWith('/_payload.json');
  const head = injectHead();
  const robotsHeader = useResponseHeader('X-Robots-Tag');
  if (payload) robotsHeader.value = 'noindex';
  head.hooks?.hook('tags:afterResolve', ({ tags }) => {
    const robots = tags.find(tag => tag.tag === 'meta' && tag.props.name === 'robots');
    if (payload) {
      robotsHeader.value = 'noindex';
    } else if (typeof robots?.props.content === 'string' && robots.props.content) {
      robotsHeader.value = robots.props.content;
    }
  });
});
