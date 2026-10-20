import { describe, expect, it } from 'vitest';
import type { CategoryNode } from '../app/utils/categories';
import { findCategoryPath } from '../app/utils/categories';
import { categoryFamily, otherCategories } from '../app/utils/categoryLinks';

const node = (slug: string, children: CategoryNode[] = [], patch: Partial<CategoryNode> = {}): CategoryNode => ({
  slug,
  name: slug,
  icon: 'lucide:code-xml',
  appliesTo: 'cask',
  hiddenByDefault: false,
  packageCount: 5,
  children,
  ...patch
});

const tree = [
  node('developer-tools', [node('text-editors'), node('terminal'), node('legacy', [], { hiddenByDefault: true })]),
  node('browsers'),
  node('empty', [], { packageCount: 0 }),
  node('hidden', [], { hiddenByDefault: true })
];

describe('Category cross-links (T1)', () => {
  it('Links a parent to its visible subcategories', () => {
    const family = categoryFamily(findCategoryPath(tree, 'developer-tools'));
    expect(family?.parent.slug).toBe('developer-tools');
    expect(family?.children.map(item => item.slug)).toEqual(['text-editors', 'terminal']);
  });

  it('Links a subcategory to its parent and siblings, a hidden one only on its own page', () => {
    expect(categoryFamily(findCategoryPath(tree, 'terminal'))?.children.map(item => item.slug)).toEqual([
      'text-editors',
      'terminal'
    ]);
    expect(categoryFamily(findCategoryPath(tree, 'legacy'))?.children.map(item => item.slug)).toEqual([
      'text-editors',
      'terminal',
      'legacy'
    ]);
    expect(categoryFamily(findCategoryPath(tree, 'browsers'))).toBeNull();
    expect(categoryFamily([])).toBeNull();
  });

  it('Links the other top-level categories that list apps', () => {
    expect(otherCategories(tree, findCategoryPath(tree, 'terminal')).map(item => item.slug)).toEqual(['browsers']);
    expect(otherCategories(tree, findCategoryPath(tree, 'browsers')).map(item => item.slug)).toEqual([
      'developer-tools'
    ]);
  });
});
