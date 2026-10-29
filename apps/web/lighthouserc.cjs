// Lighthouse acceptance for details pages (10 M2-08: Performance ≥ 90, SEO = 100, Accessibility ≥ 95; default mobile throttling).
// Measure the production build behind scripts/compress-proxy.mjs (3100, simulating production Caddy compression); see 05 §8.
// CI CPU performance varies widely, so performance scores only warn there; SEO and accessibility must pass in every environment.
const performance = process.env.CI ? 'warn' : 'error';
const median = { aggregationMethod: 'median-run' };

module.exports = {
  ci: {
    collect: {
      settings: {
        // Lighthouse 12.6.1's DIRECTIVE_SAFELIST does not recognize Content-Signal.
        // TODO: restore the robots-txt audit once Lighthouse supports Content-Signal.
        skipAudits: ['robots-txt']
      },
      url: [
        'http://localhost:3100/zh/apps/visual-studio-code',
        'http://localhost:3100/apps/visual-studio-code',
        'http://localhost:3100/apps/ghostty'
      ],
      numberOfRuns: 3
    },
    assert: {
      assertions: {
        'categories:performance': [performance, { minScore: 0.9, ...median }],
        'categories:seo': ['error', { minScore: 1, ...median }],
        'categories:accessibility': ['error', { minScore: 0.95, ...median }]
      }
    },
    upload: { target: 'filesystem', outputDir: '.lighthouseci/reports' }
  }
};
