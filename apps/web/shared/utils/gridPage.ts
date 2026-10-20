// Grid lists (all apps, a category, recently updated) have one page size on the web (S2): the URL
// never carries a size, so a list has exactly one set of pages for crawlers and visitors alike.

/** Items per page of a grid list. */
export const GRID_PAGE_SIZE = 24;

/** Grid list paths, with or without a locale prefix (zh, ja, …, zh-hant). */
const GRID_LIST = /^(?:\/[a-z]{2}(?:-[a-z]{4})?)?\/(?:apps|recent|categories\/[^/]+)$/;

export const isGridListPath = (path: string) => GRID_LIST.test(path);
