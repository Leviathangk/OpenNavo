/* Generated from tokens.json by packages/tokens/scripts/generate.ts; do not edit. After changing tokens.json, run `pnpm --filter @opennavo/tokens gen`. */
import type { Preset } from 'unocss';

/** Colors, radii, shadows reference token CSS variables; pages must import @opennavo/tokens/tokens.css. */
export const theme = {
  "colors": {
    "surface": {
      "page": "var(--on-surface-page)",
      "base": "var(--on-surface-base)",
      "sidebar": "var(--on-surface-sidebar)",
      "rail": "var(--on-surface-rail)",
      "card": "var(--on-surface-card)",
      "card-alt": "var(--on-surface-card-alt)",
      "raised": "var(--on-surface-raised)",
      "chip": "var(--on-surface-chip)",
      "control": "var(--on-surface-control)",
      "inset": "var(--on-surface-inset)",
      "overlay": "var(--on-surface-overlay)",
      "popover": "var(--on-surface-popover)"
    },
    "ink": {
      "primary": "var(--on-text-primary)",
      "secondary": "var(--on-text-secondary)",
      "tertiary": "var(--on-text-tertiary)",
      "disabled": "var(--on-text-disabled)",
      "inverse": "var(--on-text-inverse)",
      "on-accent": "var(--on-text-on-accent)",
      "accent": "var(--on-text-accent)"
    },
    "line": {
      "subtle": "var(--on-border-subtle)",
      "default": "var(--on-border-default)",
      "strong": "var(--on-border-strong)",
      "sidebar": "var(--on-border-sidebar)"
    },
    "brand": {
      "coral": "var(--on-brand-coral)",
      "salmon": "var(--on-brand-salmon)",
      "violet": "var(--on-brand-violet)",
      "glow-core": "var(--on-brand-glow-core)"
    },
    "accent": {
      "coral-subtle": "var(--on-accent-coral-subtle)",
      "salmon-subtle": "var(--on-accent-salmon-subtle)"
    },
    "status": {
      "success": "var(--on-status-success)",
      "success-subtle": "var(--on-status-success-subtle)",
      "warning": "var(--on-status-warning)",
      "warning-subtle": "var(--on-status-warning-subtle)",
      "danger": "var(--on-status-danger)",
      "danger-subtle": "var(--on-status-danger-subtle)",
      "info": "var(--on-status-info)",
      "info-subtle": "var(--on-status-info-subtle)",
      "success-border": "var(--on-status-success-border)",
      "warning-border": "var(--on-status-warning-border)",
      "danger-border": "var(--on-status-danger-border)",
      "info-border": "var(--on-status-info-border)"
    },
    "button": {
      "primary-bg": "var(--on-button-primary-bg)",
      "primary-hover": "var(--on-button-primary-hover)",
      "primary-pressed": "var(--on-button-primary-pressed)",
      "primary-text": "var(--on-button-primary-text)",
      "secondary-bg": "var(--on-button-secondary-bg)",
      "secondary-hover": "var(--on-button-secondary-hover)",
      "secondary-pressed": "var(--on-button-secondary-pressed)",
      "secondary-text": "var(--on-button-secondary-text)",
      "accent-bg": "var(--on-button-accent-bg)",
      "accent-hover": "var(--on-button-accent-hover)",
      "accent-pressed": "var(--on-button-accent-pressed)",
      "accent-text": "var(--on-button-accent-text)",
      "disabled-bg": "var(--on-button-disabled-bg)",
      "disabled-text": "var(--on-button-disabled-text)"
    },
    "chart": {
      "series": {
        "1": "var(--on-chart-series1)",
        "2": "var(--on-chart-series2)",
        "3": "var(--on-chart-series3)"
      },
      "emphasis": "var(--on-chart-emphasis)",
      "deemphasis": "var(--on-chart-deemphasis)",
      "grid": "var(--on-chart-grid)",
      "axis": "var(--on-chart-axis)"
    },
    "component": {
      "search-bg": "var(--on-component-search-bg)",
      "card-hover": "var(--on-component-card-hover)",
      "toggle-track": "var(--on-component-toggle-track)",
      "toggle-knob": "var(--on-component-toggle-knob)",
      "toggle-knob-on": "var(--on-component-toggle-knob-on)",
      "tab-indicator": "var(--on-component-tab-indicator)",
      "nav-active": "var(--on-component-nav-active)",
      "hero-bg": "var(--on-component-hero-bg)",
      "caption-bg": "var(--on-component-caption-bg)",
      "top-nav-scrolled": "var(--on-component-top-nav-scrolled)",
      "scrim": "var(--on-component-scrim)",
      "lightbox-scrim": "var(--on-component-lightbox-scrim)",
      "modal-bg": "var(--on-component-modal-bg)",
      "toast-bg": "var(--on-component-toast-bg)",
      "item-active": "var(--on-component-item-active)",
      "running-border": "var(--on-component-running-border)",
      "log-heading": "var(--on-component-log-heading)",
      "log-success": "var(--on-component-log-success)",
      "mono-text": "var(--on-component-mono-text)",
      "timeline-line": "var(--on-component-timeline-line)",
      "timeline-dot": "var(--on-component-timeline-dot)",
      "source-meta": "var(--on-component-source-meta)",
      "collection-from": "var(--on-component-collection-from)",
      "collection-to": "var(--on-component-collection-to)",
      "collection-ring": "var(--on-component-collection-ring)",
      "detail-header-bg": "var(--on-component-detail-header-bg)",
      "cli-icon-from": "var(--on-component-cli-icon-from)",
      "cli-icon-to": "var(--on-component-cli-icon-to)",
      "cli-icon-text": "var(--on-component-cli-icon-text)",
      "letter-icon-text": "var(--on-component-letter-icon-text)",
      "segment-active": "var(--on-component-segment-active)",
      "menu-bar-bg": "var(--on-component-menu-bar-bg)",
      "menu-bar-text": "var(--on-component-menu-bar-text)"
    },
    "macos": {
      "close": "var(--on-macos-close)",
      "minimize": "var(--on-macos-minimize)",
      "zoom": "var(--on-macos-zoom)"
    }
  },
  "borderRadius": {
    "tiny": "var(--on-radius-tiny)",
    "small": "var(--on-radius-small)",
    "default": "var(--on-radius-default)",
    "panel": "var(--on-radius-panel)",
    "big": "var(--on-radius-big)",
    "huge": "var(--on-radius-huge)",
    "xl": "var(--on-radius-xl)",
    "full": "var(--on-radius-full)",
    "app-icon": "var(--on-radius-app-icon)"
  },
  "boxShadow": {
    "window": "var(--on-shadow-window)",
    "popover": "var(--on-shadow-popover)",
    "modal": "var(--on-shadow-modal)",
    "toast": "var(--on-shadow-toast)",
    "float-icon": "var(--on-shadow-float-icon)",
    "app-icon": "var(--on-shadow-app-icon)",
    "icon-text": "var(--on-shadow-icon-text)",
    "focus-ring": "var(--on-shadow-focus-ring)",
    "segment": "var(--on-shadow-segment)",
    "knob": "var(--on-shadow-knob)"
  },
  "fontFamily": {
    "sans": "var(--on-font-family-sans)",
    "sans-zh": "var(--on-font-family-sans-zh)",
    "sans-ja": "var(--on-font-family-sans-ja)",
    "mono": "var(--on-font-family-mono)"
  },
  "duration": {
    "fast": "var(--on-motion-duration-fast)",
    "base": "var(--on-motion-duration-base)",
    "slow": "var(--on-motion-duration-slow)"
  },
  "easing": {
    "standard": "var(--on-motion-easing-standard)",
    "emphasized": "var(--on-motion-easing-emphasized)",
    "exit": "var(--on-motion-easing-exit)"
  },
  "zIndex": {
    "base": "var(--on-z-index-base)",
    "sticky": "var(--on-z-index-sticky)",
    "dropdown": "var(--on-z-index-dropdown)",
    "overlay": "var(--on-z-index-overlay)",
    "modal": "var(--on-z-index-modal)",
    "popover": "var(--on-z-index-popover)",
    "toast": "var(--on-z-index-toast)",
    "tooltip": "var(--on-z-index-tooltip)"
  },
  "breakpoints": {
    "sm": "640px",
    "md": "768px",
    "lg": "1024px",
    "xl": "1280px",
    "2xl": "1440px"
  }
};

/** Typography text-{name} classes set font size, line height, weight, and tracking together (08 §4.2). */
export const typography: Record<string, Record<string, string>> = {
  "hero": {
    "font-size": "var(--on-typography-hero-size)",
    "line-height": "var(--on-typography-hero-line-height)",
    "font-weight": "var(--on-typography-hero-weight)",
    "letter-spacing": "var(--on-typography-hero-letter-spacing)"
  },
  "section": {
    "font-size": "var(--on-typography-section-size)",
    "line-height": "var(--on-typography-section-line-height)",
    "font-weight": "var(--on-typography-section-weight)",
    "letter-spacing": "var(--on-typography-section-letter-spacing)"
  },
  "display": {
    "font-size": "var(--on-typography-display-size)",
    "line-height": "var(--on-typography-display-line-height)",
    "font-weight": "var(--on-typography-display-weight)",
    "letter-spacing": "var(--on-typography-display-letter-spacing)"
  },
  "title1": {
    "font-size": "var(--on-typography-title1-size)",
    "line-height": "var(--on-typography-title1-line-height)",
    "font-weight": "var(--on-typography-title1-weight)",
    "letter-spacing": "var(--on-typography-title1-letter-spacing)"
  },
  "title2": {
    "font-size": "var(--on-typography-title2-size)",
    "line-height": "var(--on-typography-title2-line-height)",
    "font-weight": "var(--on-typography-title2-weight)",
    "letter-spacing": "var(--on-typography-title2-letter-spacing)"
  },
  "headline": {
    "font-size": "var(--on-typography-headline-size)",
    "line-height": "var(--on-typography-headline-line-height)",
    "font-weight": "var(--on-typography-headline-weight)",
    "letter-spacing": "var(--on-typography-headline-letter-spacing)"
  },
  "stat": {
    "font-size": "var(--on-typography-stat-value-size)",
    "line-height": "var(--on-typography-stat-value-line-height)",
    "font-weight": "var(--on-typography-stat-value-weight)",
    "letter-spacing": "var(--on-typography-stat-value-letter-spacing)"
  },
  "body": {
    "font-size": "var(--on-typography-body-size)",
    "line-height": "var(--on-typography-body-line-height)",
    "font-weight": "var(--on-typography-body-weight)",
    "letter-spacing": "0"
  },
  "body-sm": {
    "font-size": "var(--on-typography-body-sm-size)",
    "line-height": "var(--on-typography-body-sm-line-height)",
    "font-weight": "var(--on-typography-body-sm-weight)",
    "letter-spacing": "0"
  },
  "caption": {
    "font-size": "var(--on-typography-caption-size)",
    "line-height": "var(--on-typography-caption-line-height)",
    "font-weight": "var(--on-typography-caption-weight)",
    "letter-spacing": "0"
  },
  "label": {
    "font-size": "var(--on-typography-label-size)",
    "line-height": "var(--on-typography-label-line-height)",
    "font-weight": "var(--on-typography-label-weight)",
    "letter-spacing": "var(--on-typography-label-letter-spacing)",
    "text-transform": "var(--on-typography-label-text-transform)"
  },
  "mono": {
    "font-size": "var(--on-typography-mono-size)",
    "line-height": "var(--on-typography-mono-line-height)",
    "font-weight": "var(--on-typography-mono-weight)",
    "letter-spacing": "0",
    "font-family": "var(--on-font-family-mono)"
  }
};

const typographyPattern = new RegExp(`^text-(${Object.keys(typography).join('|')})$`);

/** Use alongside presetWind3: presets: [presetWind3(), presetOpenNavo()]. */
export function presetOpenNavo(): Preset {
  return {
    name: '@opennavo/tokens',
    theme,
    rules: [[typographyPattern, ([, name]) => (name ? typography[name] : undefined)]]
  };
}
