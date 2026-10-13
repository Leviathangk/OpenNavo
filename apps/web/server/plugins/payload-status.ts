// Removed apps and withdrawn articles answer 410, and Nuxt sends their _payload.json with the same
// status. The client drops a payload that is not OK and fetches the page data again from the
// browser, so payloads of these pages keep 200; the pages themselves still answer 410.
export default defineNitroPlugin(nitroApp => {
  nitroApp.hooks.hook('render:response', response => {
    const type = String(response.headers?.['content-type'] ?? '');
    if (response.statusCode === 410 && type.startsWith('application/json')) {
      response.statusCode = 200;
      response.statusMessage = 'OK';
    }
  });
});
