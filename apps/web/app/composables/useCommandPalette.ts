/** Command-K palette state shared by navigation search and keyboard shortcut. */
export function useCommandPalette() {
  const open = useState('command-palette:open', () => false);
  return { open };
}

/**
 * Command-K / Ctrl-K toggles the palette, including from inputs. Layouts own the listener, so the palette
 * itself loads only once it is opened (LazyCommandPalette, X3) and stays out of the entry chunk.
 */
export function useCommandPaletteShortcut() {
  const { open } = useCommandPalette();
  const onKeydown = (event: KeyboardEvent) => {
    if ((event.metaKey || event.ctrlKey) && event.key.toLowerCase() === 'k') {
      event.preventDefault();
      open.value = !open.value;
    }
  };
  onMounted(() => document.addEventListener('keydown', onKeydown));
  onBeforeUnmount(() => document.removeEventListener('keydown', onKeydown));
}
