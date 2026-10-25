/**
 * The copyright year, read from the instant the server rendered the page (plugins/dates.ts) in UTC, so a page
 * cached across New Year hydrates with the year it was rendered in. Once the page is ready the clock answers.
 */
export function useCopyrightYear(): number {
  const renderedAt = useState<number | null>('onv:rendered-at', () => null);
  return new Date(renderedAt.value ?? Date.now()).getUTCFullYear();
}
