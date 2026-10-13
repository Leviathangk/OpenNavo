// Temporary rollout switch (admin "SEO & analytics" > title template). Delete together with the
// legacy title strings once the new templates have been stable for a month or two.
export function useLegacyTitles() {
  const settings = useSiteSettings();
  return computed(() => settings.value?.pages.titleTemplate === 'legacy');
}
