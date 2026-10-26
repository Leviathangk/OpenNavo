import { describe, expect, it } from 'vitest';
import { robotsAllowsIndexing } from '../app/utils/indexing';

describe('robots indexability', () => {
  it.each([
    ['index, follow', true],
    ['index, follow, max-image-preview:large, max-snippet:-1, max-video-preview:-1', true],
    [' MAX-SNIPPET:-1, INDEX , FOLLOW ', true],
    ['index, noindex, follow', false],
    ['noindex, follow, max-image-preview:large', false],
    ['follow, max-snippet:-1', false],
    ['noindex', false],
    ['', false],
    [undefined, false],
    [null, false]
  ])('interprets %s as %s', (robots, expected) => {
    expect(robotsAllowsIndexing(robots)).toBe(expected);
  });
});
