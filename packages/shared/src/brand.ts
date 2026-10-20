// Central brand constants (provisional OpenNavo); renaming needs only these, language resources, and logo assets.
// Official domains/Bundle IDs require manual decisions (H-01, H-02); use placeholders until then.
import { color } from '@opennavo/tokens';

export const BRAND = {
  name: 'OpenNavo',
  /** Deep-link scheme (06 §10). */
  scheme: 'opennavo',
  /** macOS Bundle ID placeholder (H-01). */
  bundleId: 'com.opennavo.desktop',
  /** Project Homebrew tap/client cask; confirm at release before listing. */
  tap: 'opennavo/tap',
  caskToken: 'opennavo',
  /** SPDX license of this repository; the root package.json is the authority and a test keeps them equal. */
  license: 'GPL-3.0-only',
  /** The public source repository. */
  repository: 'https://github.com/opennavo/OpenNavo'
} as const;

/**
 * Logo: N plus Polaris in a 24×24 view, shared across clients; source/construction in docs/design/logo/.
 * N uses outerGradient; Polaris uses innerGradient on dark backgrounds and innerGradientOnLight on light ones.
 */
export const LOGO = {
  /** Square viewbox, optically centered, for standalone favicon/loading uses. */
  viewBox: '0 0 24 24',
  /** Tight viewbox for inline text; square bounds add about one-fifth width of whitespace on each side. */
  markViewBox: '4.562 0 15.809 24',
  /** markViewBox aspect ratio: width = height × markAspect. */
  markAspect: 0.6587,
  /** Square Polaris-only viewbox for decorative badges, used with innerPath. */
  starViewBox: '10.744 0 11.317 11.317',
  /**
   * N center's downward offset as a fraction of viewbox height; viewBox and markViewBox have equal heights.
   * Bounds center N plus star, whose top occupies space; raise inline logos by this amount to center N with text.
   */
  nCenterOffset: 0.1541,
  /** Polaris area-centroid offset below starViewBox center as a side fraction; its long top ray stretches bounds, so align inline by area centroid. */
  starCenterOffset: 0.0768,
  /** Letter N. */
  outerPath:
    'M5.507,23.52A0.465,0.465 0 0 1 5.042,23.055L5.042,9.839A1.961,1.961 0 0 1 7.003,7.878L8.237,7.878A1.268,1.268 0 0 1 9.331,8.504L14.266,16.909A0.211,0.211 0 0 0 14.659,16.802L14.659,13.205A0.465,0.465 0 0 1 15.124,12.74L17.682,12.74A0.465,0.465 0 0 1 18.147,13.205L18.147,21.559A1.961,1.961 0 0 1 16.186,23.52L14.951,23.52A1.268,1.268 0 0 1 13.857,22.894L8.923,14.489A0.211,0.211 0 0 0 8.529,14.596L8.529,23.055A0.465,0.465 0 0 1 8.064,23.52Z',
  /** Polaris vertical axis aligns with the center of N's right stem. */
  innerPath:
    'M16.403,0.48C16.403,4.133 18.007,7.244 19.891,7.244C18.007,7.244 16.403,8.897 16.403,10.837C16.403,8.897 14.799,7.244 12.915,7.244C14.799,7.244 16.403,4.133 16.403,0.48Z',
  /** At 32 px and below, generate viewbox and centering offsets from small-size geometry. */
  small: {
    viewBox: '0 0 24 24',
    markViewBox: '4.075 0 16.978 24',
    markAspect: 0.7074,
    starViewBox: '10.878 0 11.273 11.273',
    nCenterOffset: 0.1417,
    starCenterOffset: 0.0664,
    outerPath:
      'M4.885,23.52A0.329,0.329 0 0 1 4.555,23.191L4.555,9.038A1.755,1.755 0 0 1 6.311,7.282L8.624,7.282A0.878,0.878 0 0 1 9.382,7.718L14.225,16.021A0.11,0.11 0 0 0 14.43,15.966L14.43,13.536A0.329,0.329 0 0 1 14.759,13.207L18.27,13.207A0.329,0.329 0 0 1 18.599,13.536L18.599,21.765A1.755,1.755 0 0 1 16.843,23.52L14.531,23.52A0.878,0.878 0 0 1 13.772,23.084L8.929,14.781A0.11,0.11 0 0 0 8.725,14.836L8.725,23.191A0.329,0.329 0 0 1 8.395,23.52Z',
    innerPath:
      'M16.514,0.48C16.514,3.64 18.625,7.063 20.574,7.063C18.625,7.063 16.514,9.003 16.514,10.793C16.514,9.003 14.403,7.063 12.455,7.063C14.403,7.063 16.514,3.64 16.514,0.48Z'
  },
  /** N gradient, top-left to bottom-right, middle stop at 55%. */
  outerGradient: [
    { offset: 0, color: color.brand.logoGradient[0] },
    { offset: 0.55, color: color.brand.logoGradient[1] },
    { offset: 1, color: color.brand.logoGradient[2] }
  ],
  /** Polaris on dark backgrounds. */
  innerGradient: [
    { offset: 0, color: color.brand.logoCoreGradient[0] },
    { offset: 1, color: color.brand.logoCoreGradient[1] }
  ],
  /** Polaris on light backgrounds, where warm white disappears. */
  innerGradientOnLight: [
    { offset: 0, color: color.brand.logoCoreGradientOnLight[0] },
    { offset: 1, color: color.brand.logoCoreGradientOnLight[1] }
  ]
} as const;

/**
 * CSS transform for logos/Polaris inline with text in items-center parents; percentages refer to the element's own height.
 * Use LOGO.nCenterOffset for the full mark or LOGO.starCenterOffset for Polaris alone.
 */
export function logoInlineShift(offset: number): string {
  return `translateY(-${(offset * 100).toFixed(2)}%)`;
}

/**
 * Generate standalone logo SVG text for loading screens/favicons outside Vue.
 * idPrefix prevents gradient collisions; background lightness determines Polaris color.
 */
export function logoSvg(idPrefix = 'onv-logo', background: 'dark' | 'light' = 'dark'): string {
  const stops = (list: ReadonlyArray<{ offset: number; color: string }>) =>
    list.map(stop => `<stop offset="${stop.offset}" stop-color="${stop.color}"/>`).join('');
  return [
    `<svg xmlns="http://www.w3.org/2000/svg" viewBox="${LOGO.viewBox}">`,
    '<defs>',
    `<linearGradient id="${idPrefix}-outer" x1="0" y1="0" x2="1" y2="1">${stops(LOGO.outerGradient)}</linearGradient>`,
    `<linearGradient id="${idPrefix}-inner" x1="0" y1="0" x2="1" y2="1">${stops(background === 'light' ? LOGO.innerGradientOnLight : LOGO.innerGradient)}</linearGradient>`,
    '</defs>',
    `<path fill="url(#${idPrefix}-outer)" d="${LOGO.outerPath}"/>`,
    `<path fill="url(#${idPrefix}-inner)" d="${LOGO.innerPath}"/>`,
    '</svg>'
  ].join('');
}
