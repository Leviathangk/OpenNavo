// Category cross-links (T1), pure for testing: every category page links its parent, its children and its
// siblings as real links, and the other top-level categories, so the tree is crawlable from any of its pages.
import type { CategoryNode } from './categories';

export interface CategoryFamily {
  /** The category the row starts with ("All …"): the page itself when it has children, else its parent. */
  parent: CategoryNode;
  /** Its subcategories; hidden ones only when one is the page itself. */
  children: CategoryNode[];
}

/**
 * The family row of a category page, from the root-to-node path (findCategoryPath): a category with
 * subcategories lists them, a subcategory lists its parent and siblings. Null for a top-level category
 * without subcategories.
 */
export function categoryFamily(path: readonly CategoryNode[]): CategoryFamily | null {
  const current = path.at(-1);
  if (!current) return null;
  const parent = current.children.length ? current : path.at(-2);
  if (!parent) return null;
  const children = parent.children.filter(node => !node.hiddenByDefault || node.slug === current.slug);
  return children.length ? { parent, children } : null;
}

/** Top-level categories other than the page's own branch that list apps, in tree order. */
export function otherCategories(tree: readonly CategoryNode[], path: readonly CategoryNode[]): CategoryNode[] {
  const root = path[0]?.slug;
  return tree.filter(node => node.slug !== root && !node.hiddenByDefault && node.packageCount > 0);
}
