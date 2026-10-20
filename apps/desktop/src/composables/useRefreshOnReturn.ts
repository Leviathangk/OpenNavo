import { onBeforeUnmount, onMounted } from 'vue';

/**
 * Run `refresh` whenever the user comes back to the window, usually from System Settings or Terminal, where they changed
 * something this view mirrors. It listens only while the calling component is mounted, and failures stay silent: the
 * view keeps what it showed.
 */
export function useRefreshOnReturn(refresh: () => unknown) {
  const check = () => {
    if (document.visibilityState === 'visible')
      void Promise.resolve()
        .then(refresh)
        .catch(() => undefined);
  };
  onMounted(() => {
    window.addEventListener('focus', check);
    document.addEventListener('visibilitychange', check);
  });
  onBeforeUnmount(() => {
    window.removeEventListener('focus', check);
    document.removeEventListener('visibilitychange', check);
  });
}
