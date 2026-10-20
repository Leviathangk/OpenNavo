// Pagination (08 §8.28): at most max slots including ellipses, always first/last/current pages. Web lists add
// jump slots (T1): pages a fixed distance before and after the current one, so deep pages are a few clicks away.

export type PaginationItem = { type: 'page'; page: number } | { type: 'ellipsis'; key: string };

function baseRange(page: number, pageCount: number, max: number): PaginationItem[] {
  const slots = Math.max(5, max);
  const pages = (from: number, to: number): PaginationItem[] =>
    Array.from({ length: to - from + 1 }, (_, index) => ({ type: 'page', page: from + index }));

  if (pageCount <= slots) return pages(1, pageCount);

  // Reserve slots for first/last and both ellipses; use the middle for current-page neighbors.
  const middle = slots - 4;
  const half = Math.floor(middle / 2);
  let start = page - half;
  let end = start + middle - 1;
  if (start <= 3) {
    return [...pages(1, slots - 2), { type: 'ellipsis', key: 'end' }, { type: 'page', page: pageCount }];
  }
  if (end >= pageCount - 2) {
    return [
      { type: 'page', page: 1 },
      { type: 'ellipsis', key: 'start' },
      ...pages(pageCount - (slots - 3), pageCount)
    ];
  }
  start = Math.max(start, 3);
  end = Math.min(end, pageCount - 2);
  return [
    { type: 'page', page: 1 },
    { type: 'ellipsis', key: 'start' },
    ...pages(start, end),
    { type: 'ellipsis', key: 'end' },
    { type: 'page', page: pageCount }
  ];
}

/**
 * Page slots for a list. `jumps` adds the pages that many steps before and after the current page (for
 * example [10, 100]); a gap of one page shows that page, longer gaps an ellipsis.
 */
export function paginationRange(
  current: number,
  total: number,
  max = 7,
  jumps: readonly number[] = []
): PaginationItem[] {
  const pageCount = Math.max(1, Math.floor(total));
  const page = Math.min(Math.max(1, Math.floor(current)), pageCount);
  const base = baseRange(page, pageCount, max);
  const shown = new Set(base.flatMap(item => (item.type === 'page' ? [item.page] : [])));
  const extra = jumps
    .filter(step => Number.isInteger(step) && step > 1)
    .flatMap(step => [page - step, page + step])
    .filter(target => target > 1 && target < pageCount && !shown.has(target));
  if (!extra.length) return base;
  const pages = [...new Set([...shown, ...extra])].sort((a, b) => a - b);
  const items: PaginationItem[] = [];
  let previous = 0;
  for (const target of pages) {
    if (target - previous === 2) items.push({ type: 'page', page: target - 1 });
    else if (previous && target - previous > 2) items.push({ type: 'ellipsis', key: `gap-${previous}` });
    items.push({ type: 'page', page: target });
    previous = target;
  }
  return items;
}
