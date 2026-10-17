import { execFileSync } from 'node:child_process';
import { fileURLToPath } from 'node:url';
import { describe, expect, it } from 'vitest';
import { BRAND, LOGO, logoInlineShift, logoSvg } from '../src/brand';
import { interpolate, sharedMessages } from '../src/messages';
import { LOCALES, PACKAGE_KINDS, isInstallableKind, isPackageKind } from '../src/types';

describe('Brand', () => {
  it('Logo generator uses tokens and matches both shared geometries', () => {
    const source = fileURLToPath(new URL('../../../docs/design/logo/source/', import.meta.url));
    execFileSync(process.execPath, ['--test', 'build.test.js'], { cwd: source });
    const generated = JSON.parse(
      execFileSync(
        process.execPath,
        [
          '-e',
          "const {logoConstants} = require('./assets'); process.stdout.write(JSON.stringify([logoConstants(), logoConstants(true)]));"
        ],
        { cwd: source, encoding: 'utf8' }
      )
    ) as Array<typeof LOGO.small>;
    expect(generated[1]).toEqual(LOGO.small);
    for (const [key, value] of Object.entries(generated[0] ?? {})) {
      expect(LOGO[key as keyof typeof LOGO]).toEqual(value);
    }
    expect(LOGO.small.outerPath).not.toBe(LOGO.outerPath);
  });

  it('Scheme and Bundle ID placeholders', () => {
    expect(BRAND.scheme).toBe('opennavo');
    expect(BRAND.bundleId).toMatch(/^com\.opennavo\./);
  });

  it('Logo SVG uses token gradients with prefixed IDs', () => {
    const svg = logoSvg('loading');
    expect(svg).toContain('id="loading-outer"');
    expect(svg).toContain('fill="url(#loading-inner)"');
    expect(svg).toContain('stop-color="#FF7356"');
    expect(svg).toContain(LOGO.outerPath);
    expect(svg).toContain(LOGO.innerPath);
    expect(svg).toContain(`stop-color="${LOGO.innerGradient[0].color}"`);
    expect(logoSvg()).toContain('id="onv-logo-outer"');
  });

  it('Light backgrounds use amber Polaris gradient', () => {
    const svg = logoSvg('admin', 'light');
    expect(svg).toContain(`stop-color="${LOGO.innerGradientOnLight[1].color}"`);
    expect(svg).not.toContain(`stop-color="${LOGO.innerGradient[0].color}"`);
  });

  it('Tight viewbox matches aspect ratio; standalone star is square', () => {
    const [, , width = 0, height = 1] = LOGO.markViewBox.split(' ').map(Number);
    expect(LOGO.markAspect).toBeCloseTo(width / height, 4);
    const [, , starWidth, starHeight] = LOGO.starViewBox.split(' ').map(Number);
    expect(starWidth).toBe(starHeight);
  });

  it('Inline upward shift matches path geometry', () => {
    // Square and tight viewboxes have equal heights; the same upward-shift ratio applies to both.
    const [, top = 0, , height = 1] = LOGO.viewBox.split(' ').map(Number);
    const [, markTop, , markHeight] = LOGO.markViewBox.split(' ').map(Number);
    expect([markTop, markHeight]).toEqual([top, height]);
    const ys = pathPoints(LOGO.outerPath).map(([, y]) => y);
    const nMiddle = (Math.min(...ys) + Math.max(...ys)) / 2;
    expect(LOGO.nCenterOffset).toBeCloseTo((nMiddle - (top + height / 2)) / height, 4);

    const [, starTop = 0, , side = 1] = LOGO.starViewBox.split(' ').map(Number);
    const starCenter = centroidY(pathPoints(LOGO.innerPath));
    expect(LOGO.starCenterOffset).toBeCloseTo((starCenter - (starTop + side / 2)) / side, 3);

    expect(logoInlineShift(LOGO.nCenterOffset)).toBe('translateY(-15.41%)');
    expect(logoInlineShift(LOGO.starCenterOffset)).toBe('translateY(-7.68%)');
  });
});

// Logo paths use only absolute M/L/A/C/Z commands from docs/design/logo/source/geom.js.
// Use segment endpoints, subdividing cubic Beziers into steps; corner arcs' endpoints already mark outer extrema.
function pathPoints(d: string, steps = 64): Array<[number, number]> {
  const tokens = d.match(/[MLACZ]|-?\d*\.?\d+/g) ?? [];
  const points: Array<[number, number]> = [];
  let command = '';
  let index = 0;
  const take = (count: number) => tokens.slice(index, (index += count)).map(Number);
  while (index < tokens.length) {
    const token = tokens[index] ?? '';
    if (/[MLACZ]/.test(token)) {
      command = token;
      index += 1;
      continue;
    }
    if (command === 'M' || command === 'L') {
      const [x = 0, y = 0] = take(2);
      points.push([x, y]);
    } else if (command === 'A') {
      const [, , , , , x = 0, y = 0] = take(7);
      points.push([x, y]);
    } else {
      const [x1 = 0, y1 = 0, x2 = 0, y2 = 0, x = 0, y = 0] = take(6);
      const [x0, y0] = points.at(-1) ?? [x, y];
      for (let step = 1; step <= steps; step += 1) {
        const t = step / steps;
        const u = 1 - t;
        points.push([
          u * u * u * x0 + 3 * u * u * t * x1 + 3 * u * t * t * x2 + t * t * t * x,
          u * u * u * y0 + 3 * u * u * t * y1 + 3 * u * t * t * y2 + t * t * t * y
        ]);
      }
    }
  }
  return points;
}

// Polygon centroid y by the shoelace formula.
function centroidY(points: Array<[number, number]>): number {
  let twiceArea = 0;
  let moment = 0;
  points.forEach(([x0, y0], index) => {
    const [x1, y1] = points[(index + 1) % points.length] ?? [x0, y0];
    const cross = x0 * y1 - x1 * y0;
    twiceArea += cross;
    moment += (y0 + y1) * cross;
  });
  return moment / (3 * twiceArea);
}

describe('Base messages', () => {
  const keys = (value: object, prefix = ''): string[] =>
    Object.entries(value).flatMap(([key, child]) =>
      typeof child === 'object' && child !== null ? keys(child as object, `${prefix}${key}.`) : [`${prefix}${key}`]
    );

  it('Chinese/English keys match and are populated', () => {
    expect(keys(sharedMessages['en-US'])).toEqual(keys(sharedMessages['zh-CN']));
    for (const locale of LOCALES) {
      for (const value of Object.values(sharedMessages[locale]).flatMap(group => Object.values(group))) {
        expect(value).not.toBe('');
      }
    }
  });

  it('Interpolation', () => {
    expect(interpolate(sharedMessages['zh-CN'].action.updateAll, { count: 6 })).toBe('全部更新（6）');
    expect(interpolate('更新到 {version}', {})).toBe('更新到 {version}');
    expect(interpolate('无占位符')).toBe('无占位符');
  });

  it('Package kinds', () => {
    expect(PACKAGE_KINDS).toEqual(['cask', 'formula', 'link']);
    expect(isPackageKind('cask')).toBe(true);
    expect(isPackageKind('link')).toBe(true);
    expect(isPackageKind('tap')).toBe(false);
    expect(isInstallableKind('link')).toBe(false);
    expect(isInstallableKind('cask')).toBe(true);
  });
});
