import { fileURLToPath } from 'node:url';
import { defineConfig } from 'vitest/config';
import vue from '@vitejs/plugin-vue';

// Unit tests include real Vue fact blocks (G5); built-page HTTP validation is in ssr-seo.test.mjs.
// The aliases mirror Nuxt's for the shared directory (#shared, ~~/shared), which app code imports from.
export default defineConfig({
  plugins: [vue()],
  resolve: {
    alias: {
      '~': fileURLToPath(new URL('./app', import.meta.url)),
      '#shared': fileURLToPath(new URL('./shared', import.meta.url)),
      '~~': fileURLToPath(new URL('.', import.meta.url))
    }
  },
  test: {
    include: ['tests/**/*.test.ts']
  }
});
