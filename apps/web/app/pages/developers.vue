<script setup lang="ts">
import { OnIcon } from '@opennavo/ui';

const { t, locale } = useI18n();

const REMOTE_MCP_CONFIG =
  '{\n  "mcpServers": {\n    "opennavo": {\n      "url": "https://opennavo.com/mcp"\n    }\n  }\n}';
const CLAUDE_CODE_COMMAND = 'claude mcp add --transport http opennavo https://opennavo.com/mcp';
const CODEX_CONFIG = '[mcp_servers.opennavo]\nurl = "https://opennavo.com/mcp"';
const CURL_EXAMPLE =
  "curl --get 'https://api.opennavo.com/api/v1/search' \\\n  --data-urlencode 'q=Visual Studio Code' \\\n  --data-urlencode 'size=5'";

const mcpExamples = computed(() => [
  { key: 'claudeDesktop', title: t('developers.claudeDesktopTitle'), code: REMOTE_MCP_CONFIG },
  { key: 'claudeCode', title: t('developers.claudeCodeTitle'), code: CLAUDE_CODE_COMMAND },
  { key: 'cursor', title: t('developers.cursorTitle'), code: REMOTE_MCP_CONFIG },
  { key: 'codex', title: t('developers.codexTitle'), code: CODEX_CONFIG }
]);

usePageSeo({ title: () => t('developers.metaTitle'), description: () => t('developers.description') });
usePageSchema({ type: 'WebPage' });

defineOgImage('OgDefaultSatori', {
  locale: toAppLocale(locale.value),
  variant: 'home',
  title: t('developers.metaTitle'),
  description: t('developers.description'),
  ...useOgFrame()
});
</script>

<template>
  <div class="mx-auto box-border flex w-full max-w-920px flex-col gap-28px pt-14px pb-40px">
    <PageHeader :title="t('developers.title')" :description="t('developers.description')" />
    <p class="m-0 max-w-760px text-15px leading-[1.75] text-ink-secondary">{{ t('developers.intro') }}</p>

    <section id="mcp" aria-labelledby="developers-mcp-title" class="flex flex-col gap-16px">
      <div>
        <h2 id="developers-mcp-title" class="m-0 text-20px font-600 text-ink-primary">
          {{ t('developers.mcpTitle') }}
        </h2>
        <p class="m-0 mt-6px max-w-720px text-14px leading-[1.7] text-ink-secondary">
          {{ t('developers.mcpDescription') }}
        </p>
      </div>
      <div
        class="flex flex-col gap-6px rounded-big border border-solid border-line-subtle bg-surface-card px-16px py-14px"
      >
        <span class="text-12px font-600 uppercase tracking-[0.08em] text-ink-tertiary">{{
          t('developers.mcpEndpoint')
        }}</span>
        <!-- The endpoint only accepts POST and DELETE; a normal link would open a 405 page. -->
        <code class="w-fit max-w-full break-all font-mono text-13px text-ink-primary select-all"
          >https://opennavo.com/mcp</code
        >
      </div>
      <div class="flex flex-col gap-12px">
        <h3 class="m-0 text-16px font-600 text-ink-primary">{{ t('developers.setupTitle') }}</h3>
        <div class="grid grid-cols-1 gap-12px md:grid-cols-2">
          <article
            v-for="example in mcpExamples"
            :key="example.key"
            class="min-w-0 rounded-big border border-solid border-line-subtle bg-surface-card px-14px py-12px"
          >
            <h4 class="m-0 text-14px font-600 text-ink-primary">{{ example.title }}</h4>
            <pre
              class="m-0 mt-10px overflow-x-auto rounded-default bg-surface-inset px-12px py-10px font-mono text-12px leading-[1.65] text-ink-secondary"
            ><code>{{ example.code }}</code></pre>
          </article>
        </div>
      </div>
    </section>

    <section id="api" aria-labelledby="developers-api-title" class="flex flex-col gap-16px">
      <div>
        <h2 id="developers-api-title" class="m-0 text-20px font-600 text-ink-primary">
          {{ t('developers.apiTitle') }}
        </h2>
        <p class="m-0 mt-6px max-w-760px text-14px leading-[1.7] text-ink-secondary">
          {{ t('developers.apiDescription') }}
        </p>
      </div>
      <div class="flex flex-wrap gap-x-20px gap-y-8px text-13px">
        <a
          href="https://api.opennavo.com/api/v1/openapi.yaml"
          target="_blank"
          rel="noopener noreferrer"
          class="inline-flex items-center gap-4px rounded-tiny text-ink-primary underline-offset-3 outline-none hover:text-ink-secondary hover:underline focus-visible:shadow-focus-ring"
        >
          {{ t('developers.apiSpec') }} <OnIcon name="arrow-up-right" :size="13" />
        </a>
        <a
          href="https://opennavo.com/.well-known/api-catalog"
          target="_blank"
          rel="noopener noreferrer"
          class="inline-flex items-center gap-4px rounded-tiny text-ink-primary underline-offset-3 outline-none hover:text-ink-secondary hover:underline focus-visible:shadow-focus-ring"
        >
          {{ t('developers.apiCatalog') }} <OnIcon name="arrow-up-right" :size="13" />
        </a>
      </div>
      <div class="rounded-big border border-solid border-line-subtle bg-surface-card px-16px py-14px">
        <h3 class="m-0 text-16px font-600 text-ink-primary">{{ t('developers.curlTitle') }}</h3>
        <pre
          class="m-0 mt-10px overflow-x-auto rounded-default bg-surface-inset px-12px py-10px font-mono text-12px leading-[1.65] text-ink-secondary"
        ><code>{{ CURL_EXAMPLE }}</code></pre>
      </div>
    </section>

    <section id="rate-limits" aria-labelledby="developers-rate-title" class="flex flex-col gap-12px">
      <h2 id="developers-rate-title" class="m-0 text-20px font-600 text-ink-primary">
        {{ t('developers.rateTitle') }}
      </h2>
      <p class="m-0 text-14px leading-[1.7] text-ink-secondary">{{ t('developers.rateIntro') }}</p>
      <ul
        class="m-0 flex list-none flex-col gap-8px rounded-big border border-solid border-line-subtle bg-surface-card px-16px py-14px text-14px leading-[1.7] text-ink-secondary"
      >
        <li>{{ t('developers.rateApi') }}</li>
        <li>{{ t('developers.rateSearch') }}</li>
        <li>{{ t('developers.rateMcp') }}</li>
        <li>{{ t('developers.rateHeaders') }}</li>
      </ul>
    </section>

    <section id="terms" aria-labelledby="developers-terms-title" class="flex flex-col gap-12px">
      <h2 id="developers-terms-title" class="m-0 text-20px font-600 text-ink-primary">
        {{ t('developers.termsTitle') }}
      </h2>
      <p class="m-0 text-14px leading-[1.7] text-ink-secondary">{{ t('developers.termsIntro') }}</p>
      <ul
        class="m-0 flex list-none flex-col gap-8px rounded-big border border-solid border-line-subtle bg-surface-card px-16px py-14px text-14px leading-[1.7] text-ink-secondary"
      >
        <li>{{ t('developers.termsFairUse') }}</li>
        <li>{{ t('developers.termsAttribution') }}</li>
        <li>{{ t('developers.termsSla') }}</li>
        <li>{{ t('developers.termsScraping') }}</li>
        <li>{{ t('developers.termsReadOnly') }}</li>
      </ul>
    </section>
  </div>
</template>
