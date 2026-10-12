/** Show the home folder as ~, as Finder's Go to Folder does; Rust always sends absolute paths. /Users/Shared and
 * /Users/Guest are not anyone's home and keep their real path. */
export function displayPath(path: string): string {
  return path.replace(/^\/Users\/(?!(?:Shared|Guest)(?:\/|$))[^/]+(?=\/|$)/, '~');
}

/** Folder part (dimmed, truncates first) and last component (emphasized) of a display path. */
export function splitPath(path: string): { dir: string; base: string } {
  const shown = displayPath(path);
  const trimmed = shown.length > 1 ? shown.replace(/\/+$/, '') : shown;
  const index = trimmed.lastIndexOf('/');
  return index < 0 ? { dir: '', base: trimmed } : { dir: trimmed.slice(0, index + 1), base: trimmed.slice(index + 1) };
}
