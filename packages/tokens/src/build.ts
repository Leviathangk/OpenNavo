// Generate platform artifacts from tokens.json using pure functions; scripts write files and tests verify outputs have not been hand-edited.

export type Json = string | number | boolean | null | Json[] | { [key: string]: Json };
export type JsonObject = { [key: string]: Json };

export interface CssVariable {
  /** For example, --on-surface-card-alt. */
  name: string;
  /** Default (dark) theme value. */
  value: string;
  /** Light theme value, present only when it differs from the dark value. */
  light?: string;
  /** Path within tokens.json, e.g. ['color', 'surface', 'cardAlt']. */
  path: string[];
  desc?: string;
}

export interface Artifacts {
  'tokens.css': string;
  'tokens.ts': string;
  'uno-preset.ts': string;
  'naive-theme.ts': string;
}

const HEADER =
  'Generated from tokens.json by packages/tokens/scripts/generate.ts; do not edit. After changing tokens.json, run `pnpm --filter @opennavo/tokens gen`.';

// Unquoted generic font families and system keywords.
const GENERIC_FONTS = new Set([
  '-apple-system',
  'BlinkMacSystemFont',
  'sans-serif',
  'serif',
  'monospace',
  'ui-monospace',
  'system-ui'
]);

// Typography token UnoCSS class names (08 §7); unlisted tokens use kebab-case.
const TYPOGRAPHY_CLASS: Record<string, string> = { statValue: 'stat' };

export function kebab(key: string): string {
  return key
    .replace(/([a-z0-9])([A-Z])/g, '$1-$2')
    .replace(/\./g, '-')
    .toLowerCase();
}

function isObject(node: Json | undefined): node is JsonObject {
  return typeof node === 'object' && node !== null && !Array.isArray(node);
}

function child(node: Json | undefined, key: string): JsonObject {
  const value = isObject(node) ? node[key] : undefined;
  if (!isObject(value)) throw new Error(`tokens.json is missing object: ${key}`);
  return value;
}

/** Numeric units: px for spacing/radii/sizes, ms for durations, unitless weights/z-indices. */
function unitFor(path: string[]): string {
  const [group, second] = path;
  if (group === 'zIndex') return '';
  if (group === 'motion') return second === 'duration' ? 'ms' : '';
  if (group === 'typography') return path.at(-1) === 'weight' ? '' : 'px';
  return 'px';
}

export function fontStack(families: Json[]): string {
  return families
    .map(item => {
      const name = String(item);
      return GENERIC_FONTS.has(name) || !/[\s\d]/.test(name) ? name : `"${name}"`;
    })
    .join(', ');
}

function formatValue(value: Json, path: string[]): string {
  if (Array.isArray(value)) return path.includes('family') ? fontStack(value) : value.map(String).join(', ');
  if (typeof value === 'number') return `${value}${unitFor(path)}`;
  return String(value);
}

/** Variable names: --on-{group}-{name}; omit color level and convert camelCase to kebab-case (08 §7). */
export function variableName(path: string[]): string {
  const segments = path[0] === 'color' ? path.slice(1) : path;
  return `--on-${segments.map(kebab).join('-')}`;
}

export function collectCssVariables(source: JsonObject): CssVariable[] {
  const variables: CssVariable[] = [];

  const visit = (node: Json, path: string[], desc?: string, light?: Json) => {
    if (isObject(node) && 'value' in node) {
      const description = typeof node.desc === 'string' ? node.desc : undefined;
      visit(node.value as Json, path, description, node.light);
      return;
    }
    if (isObject(node)) {
      const entries = Object.entries(node).filter(([key]) => key !== 'desc');
      // Sort numeric keys such as spacing numerically; JavaScript objects place integer keys first.
      if (entries.every(([key]) => /^\d+(\.\d+)?$/.test(key))) entries.sort(([a], [b]) => Number(a) - Number(b));
      for (const [key, value] of entries) visit(value, [...path, key]);
      return;
    }
    const value = formatValue(node, path);
    // Placeholder templates such as detailGlow's {accent} are available only as TS functions.
    if (value.includes('{')) return;
    const lightValue = light === undefined ? undefined : formatValue(light, path);
    variables.push({
      name: variableName(path),
      value,
      ...(lightValue !== undefined && lightValue !== value ? { light: lightValue } : {}),
      path,
      ...(desc ? { desc } : {})
    });
    // Color arrays (gradients) also expose each stop, e.g. --on-brand-logo-core-gradient-1, for SVG stop-color.
    if (path[0] === 'color' && Array.isArray(node)) {
      node.forEach((stop, index) => {
        const lightStop = Array.isArray(light) ? String(light[index]) : undefined;
        variables.push({
          name: `${variableName(path)}-${index + 1}`,
          value: String(stop),
          ...(lightStop !== undefined && lightStop !== String(stop) ? { light: lightStop } : {}),
          path: [...path, String(index + 1)]
        });
      });
    }
  };

  for (const [group, node] of Object.entries(source)) {
    if (group === 'meta') continue;
    visit(node, [group]);
  }
  return variables;
}

function themeBlock(
  selector: string,
  scheme: 'dark' | 'light',
  entries: Array<[string, string]>,
  indent = ''
): string[] {
  return [
    `${indent}${selector} {`,
    `${indent}  color-scheme: ${scheme};`,
    ...entries.map(([name, value]) => `${indent}  ${name}: ${value};`),
    `${indent}}`
  ];
}

// Dark is the default theme. Light follows prefers-color-scheme unless the page sets data-theme='dark';
// data-theme on html or any element forces a theme for that subtree (08 §3.7).
export function renderCss(variables: CssVariable[]): string {
  const lines = [`/* ${HEADER} */`, ':root {', '  color-scheme: dark;', ''];
  let previousGroup = '';
  for (const variable of variables) {
    const group = variable.name.split('-')[3] ?? '';
    if (previousGroup && group !== previousGroup) lines.push('');
    previousGroup = group;
    if (variable.desc) lines.push(`  /* ${variable.desc} */`);
    lines.push(`  ${variable.name}: ${variable.value};`);
  }
  lines.push('}', '');

  // Only themed tokens are repeated; spacing, type, radii and motion are the same in both themes.
  const themed = variables.filter(variable => variable.light !== undefined);
  const light = themed.map(variable => [variable.name, variable.light ?? variable.value] as [string, string]);
  const dark = themed.map(variable => [variable.name, variable.value] as [string, string]);
  lines.push(
    "/* Light theme: follows the system (prefers-color-scheme) unless the page sets data-theme='dark'. */",
    '@media (prefers-color-scheme: light) {',
    ...themeBlock(":root:not([data-theme='dark'])", 'light', light, '  '),
    '}',
    '',
    "/* data-theme='light' forces the light theme on the page (html) or on a subtree. */",
    ...themeBlock("[data-theme='light']", 'light', light),
    '',
    "/* data-theme='dark' forces the dark theme, also for a subtree inside a light page. */",
    ...themeBlock("[data-theme='dark']", 'dark', dark),
    ''
  );
  for (const [language, name] of [
    ['ja', 'sans-ja'],
    ['zh', 'sans-zh']
  ]) {
    const font = variables.find(variable => variable.name === `--on-font-family-${name}`);
    if (font) lines.push(`:lang(${language}) { --on-font-family-sans: ${font.value}; }`, '');
  }
  return lines.join('\n');
}

/** Remove { value, light, desc } wrappers to obtain raw runtime values for one theme. */
export function unwrap(node: Json, theme: 'dark' | 'light' = 'dark'): Json {
  if (isObject(node)) {
    if ('value' in node) return unwrap((theme === 'light' && 'light' in node ? node.light : node.value) as Json, theme);
    const result: JsonObject = {};
    for (const [key, value] of Object.entries(node)) {
      if (key === 'desc') continue;
      result[key] = unwrap(value, theme);
    }
    return result;
  }
  return node;
}

function hasLight(node: Json | undefined): boolean {
  if (!isObject(node)) return false;
  if ('value' in node) return 'light' in node;
  return Object.values(node).some(hasLight);
}

function literal(value: Json): string {
  return JSON.stringify(value, null, 2);
}

export function renderTokensTs(source: JsonObject, variables: CssVariable[]): string {
  const groups = Object.keys(source).filter(group => group !== 'meta');
  const blocks = groups.map(group => `export const ${group} = ${literal(unwrap(source[group] ?? null))} as const;`);
  const lightTheme = Object.fromEntries(
    groups.filter(group => hasLight(source[group])).map(group => [group, unwrap(source[group] ?? null, 'light')])
  );
  const names = variables.map(variable => `  | '${variable.name.slice('--on-'.length)}'`).join('\n');
  const detailGlow = String(unwrap(child(source.effect, 'detailGlow')));

  return `/* ${HEADER} */

${blocks.join('\n\n')}

/** Theme names; dark is the default and the values above. */
export const themeNames = ['dark', 'light'] as const;
export type ThemeName = (typeof themeNames)[number];

/** Light theme values of the themed groups; tokens without a light value repeat the dark value. */
export const lightTheme = ${literal(lightTheme)} as const;

/** All CSS variable names without the --on- prefix. */
export type TokenVariable =
${names};

/** Reference a token CSS variable, e.g. tokenVar('surface-card') → var(--on-surface-card). */
export function tokenVar(name: TokenVariable): string {
  return \`var(--on-\${name})\`;
}

/** Upper-right detail-header glow; accent is the package's six-digit accentColor, default #3C96F5. */
export function detailGlow(accent = '#3C96F5'): string {
  return ${JSON.stringify(detailGlow)}.replaceAll('{accent}', accent);
}
`;
}

function themeGroup(variables: CssVariable[], path: string[]): Record<string, string> {
  const result: Record<string, string> = {};
  for (const variable of variables) {
    if (variable.path.length !== path.length + 1) continue;
    if (!path.every((segment, index) => variable.path[index] === segment)) continue;
    const key = variable.path.at(-1) ?? '';
    result[kebab(key)] = `var(${variable.name})`;
  }
  return result;
}

// UnoCSS splits trailing color-name digits into shades (series1 → series-1, then series.1).
// Store these keys nested in the theme, keeping class names such as bg-chart-series1.
function nestTrailingDigits(group: Record<string, string>): Record<string, string | Record<string, string>> {
  const result: Record<string, string | Record<string, string>> = {};
  for (const [key, value] of Object.entries(group)) {
    const match = /^(.*[a-z])(\d+)$/.exec(key);
    if (!match?.[1] || !match[2]) {
      result[key] = value;
      continue;
    }
    const nested = result[match[1]] ?? {};
    if (typeof nested === 'string') throw new Error(`Color key ${match[1]} conflicts with ${key}`);
    nested[match[2]] = value;
    result[match[1]] = nested;
  }
  return result;
}

export function renderUnoPreset(source: JsonObject, variables: CssVariable[]): string {
  // Gradient arrays are not single colors and do not enter the color theme.
  const brand = themeGroup(variables, ['color', 'brand']);
  for (const [key, token] of Object.entries(child(child(source, 'color'), 'brand'))) {
    if (isObject(token) && Array.isArray(token.value)) delete brand[kebab(key)];
  }

  const breakpoints: Record<string, string> = {};
  for (const [key, value] of Object.entries(child(child(source.layout, 'web'), 'breakpoints'))) {
    breakpoints[key] = `${String(value)}px`;
  }

  const theme = {
    colors: {
      surface: themeGroup(variables, ['color', 'surface']),
      ink: themeGroup(variables, ['color', 'text']),
      line: themeGroup(variables, ['color', 'border']),
      brand,
      accent: themeGroup(variables, ['color', 'accent']),
      status: themeGroup(variables, ['color', 'status']),
      button: themeGroup(variables, ['color', 'button']),
      chart: nestTrailingDigits(themeGroup(variables, ['color', 'chart'])),
      component: themeGroup(variables, ['color', 'component']),
      macos: themeGroup(variables, ['color', 'macos'])
    },
    borderRadius: themeGroup(variables, ['radius']),
    boxShadow: themeGroup(variables, ['shadow']),
    fontFamily: themeGroup(variables, ['font', 'family']),
    duration: themeGroup(variables, ['motion', 'duration']),
    easing: themeGroup(variables, ['motion', 'easing']),
    zIndex: themeGroup(variables, ['zIndex']),
    breakpoints
  };

  const typography: Record<string, Record<string, string>> = {};
  for (const [key, spec] of Object.entries(child(source, 'typography'))) {
    if (!isObject(spec)) continue;
    const base = `--on-typography-${kebab(key)}`;
    const css: Record<string, string> = {
      'font-size': `var(${base}-size)`,
      'line-height': `var(${base}-line-height)`,
      'font-weight': `var(${base}-weight)`,
      'letter-spacing': 'letterSpacing' in spec ? `var(${base}-letter-spacing)` : '0'
    };
    if ('textTransform' in spec) css['text-transform'] = `var(${base}-text-transform)`;
    if (key === 'mono') css['font-family'] = 'var(--on-font-family-mono)';
    typography[TYPOGRAPHY_CLASS[key] ?? kebab(key)] = css;
  }

  return `/* ${HEADER} */
import type { Preset } from 'unocss';

/** Colors, radii, shadows reference token CSS variables; pages must import @opennavo/tokens/tokens.css. */
export const theme = ${literal(theme)};

/** Typography text-{name} classes set font size, line height, weight, and tracking together (08 §4.2). */
export const typography: Record<string, Record<string, string>> = ${literal(typography)};

const typographyPattern = new RegExp(\`^text-(\${Object.keys(typography).join('|')})$\`);

/** Use alongside presetWind3: presets: [presetWind3(), presetOpenNavo()]. */
export function presetOpenNavo(): Preset {
  return {
    name: '@opennavo/tokens',
    theme,
    rules: [[typographyPattern, ([, name]) => (name ? typography[name] : undefined)]]
  };
}
`;
}

export function renderNaiveTheme(source: JsonObject): string {
  const color = unwrap(child(source, 'color')) as Record<string, Record<string, string>>;
  const pick = (group: string, key: string) => {
    const value = color[group]?.[key];
    if (!value) throw new Error(`tokens.json is missing color: ${group}.${key}`);
    return value;
  };
  const family = unwrap(child(source, 'font')) as { family: { sans: Json[]; mono: Json[] } };

  // Admin themeRadius controls radii centrally (08 §9.3); standardize only fonts here.
  const shared = {
    fontFamily: 'var(--on-font-family-sans)',
    fontFamilyMono: fontStack(family.family.mono)
  };

  // Solid-button text: coral uses onAccent (08 §3.5); status colors use dark text because white lacks contrast.
  const onColor = (prefix: string, value: string) =>
    Object.fromEntries(['', 'Hover', 'Pressed', 'Focus'].map(scene => [`textColor${scene}${prefix}`, value]));
  const statusButtonText = (value: string) => ({
    ...onColor('Info', value),
    ...onColor('Success', value),
    ...onColor('Warning', value),
    ...onColor('Error', value)
  });

  const dark = {
    common: {
      ...shared,
      bodyColor: pick('surface', 'base'),
      cardColor: pick('surface', 'card'),
      modalColor: pick('surface', 'card'),
      popoverColor: pick('surface', 'raised'),
      tableColor: pick('surface', 'card'),
      tableHeaderColor: pick('surface', 'cardAlt'),
      inputColor: pick('surface', 'cardAlt'),
      actionColor: pick('surface', 'cardAlt'),
      tagColor: pick('surface', 'chip'),
      textColorBase: pick('text', 'primary'),
      textColor1: pick('text', 'primary'),
      textColor2: pick('text', 'secondary'),
      textColor3: pick('text', 'tertiary'),
      textColorDisabled: pick('text', 'disabled'),
      placeholderColor: pick('text', 'tertiary'),
      dividerColor: pick('border', 'subtle'),
      borderColor: pick('border', 'default')
    },
    Button: { ...onColor('Primary', pick('text', 'onAccent')), ...statusButtonText(pick('text', 'inverse')) },
    Checkbox: { checkMarkColor: pick('text', 'onAccent') },
    Radio: { buttonTextColorActive: pick('text', 'onAccent') },
    DatePicker: { itemTextColorActive: pick('text', 'onAccent') }
  };

  const light = {
    common: shared,
    Button: statusButtonText(pick('text', 'inverse'))
  };

  const admin = {
    primaryLight: pick('admin', 'primaryLight'),
    primaryDark: pick('admin', 'primaryDark'),
    info: pick('status', 'info'),
    success: pick('status', 'success'),
    warning: pick('status', 'warning'),
    error: pick('status', 'danger'),
    layoutDark: pick('admin', 'layoutDark'),
    containerDark: pick('admin', 'containerDark'),
    textDark: pick('text', 'primary')
  };

  return `/* ${HEADER} */
import type { GlobalThemeOverrides } from 'naive-ui';

/** Admin brand/status colors (08 §3.5): primaryLight in light mode, primaryDark in dark mode. */
export const adminColors = ${literal(admin)} as const;

/** Dark NaiveUI overrides: surfaces, text, borders, fonts, dark text on colored buttons. */
export const naiveDarkOverrides: GlobalThemeOverrides = ${literal(dark)};

/** Light NaiveUI overrides: standardize fonts, use dark text on status buttons. */
export const naiveLightOverrides: GlobalThemeOverrides = ${literal(light)};
`;
}

export function buildArtifacts(source: JsonObject): Artifacts {
  const variables = collectCssVariables(source);
  return {
    'tokens.css': renderCss(variables),
    'tokens.ts': renderTokensTs(source, variables),
    'uno-preset.ts': renderUnoPreset(source, variables),
    'naive-theme.ts': renderNaiveTheme(source)
  };
}
