/**
 * Select a stable daily window from a ranked list.
 * The day is UTC so server rendered pages and desktop clients agree.
 */
export function rotateDaily<T>(items: readonly T[], windowSize: number, outputSize = windowSize, at = new Date()): T[] {
  if (outputSize <= 0 || items.length === 0) return [];
  if (windowSize <= 0 || items.length <= windowSize) return items.slice(0, outputSize);
  const day = utcDay(at);
  const offset = (day * windowSize) % items.length;
  return Array.from({ length: Math.min(outputSize, items.length) }, (_, index) => items[(offset + index) % items.length]!);
}
export function utcDay(at = new Date()): number {
  return Math.floor(at.getTime() / 86_400_000);
}
