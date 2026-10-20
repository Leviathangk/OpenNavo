<script setup lang="ts">
import { formatDate } from '@opennavo/shared';
import { displayUrl } from '@opennavo/ui';
import { isoDate, isVerified } from '~/utils/packageFacts';
import type { IntegrationFact, PackagePage } from '~/utils/packageFacts';

// {name} MCP servers and Skills (X7): verified connectors for the version on offer, with how they install,
// which clients they work with, the files and network origins they touch, Apple silicon support and their
// release history. The block is off unless the backend flag web.appIntegrationsEnabled is on: the API then
// returns facts.integrations, and empties it while the flag is off (checked on every read, cache hits too).
// The flag is not exposed to the web, so an empty list is the only signal and the block shows nothing.
const props = defineProps<{ pkg: PackagePage }>();

const { t, locale } = useI18n();
const appLocale = computed(() => toAppLocale(locale.value));

const CLIENTS: Record<IntegrationFact['clients'][number], string> = {
  claude_desktop: 'Claude Desktop',
  cursor: 'Cursor',
  codex: 'Codex'
};
const METHODS: Record<IntegrationFact['installation']['method'], string> = {
  homebrew: 'Homebrew',
  uv: 'uv',
  npm: 'npm'
};

const items = computed(() =>
  (props.pkg.facts.integrations ?? [])
    .filter(item => isVerified(item.evidence))
    .map((item, index) => ({
      key: `${index}:${item.kind}:${item.name}`,
      kind: t(`package.integrations.kinds.${item.kind}`),
      name: item.name,
      version: item.version,
      repository: item.repositoryUrl,
      install: `${METHODS[item.installation.method]} · ${item.installation.package}`,
      clients: item.clients.map(client => CLIENTS[client]).join(', '),
      files: item.permissions.files.map(file => ({
        path: file.path,
        access: t(`package.integrations.access.${file.access}`)
      })),
      network: item.permissions.network,
      silicon: item.appleSilicon === 'unknown' ? '' : t(`package.integrations.silicon.${item.appleSilicon}`),
      versions: item.versions.map(entry => ({
        version: entry.version,
        url: entry.sourceUrl,
        iso: isoDate(entry.releasedAt),
        date: formatDate(entry.releasedAt, { locale: appLocale.value })
      })),
      source: item.evidence.sourceUrl,
      checked: item.evidence.checkedAt
        ? {
            iso: isoDate(item.evidence.checkedAt),
            date: formatDate(item.evidence.checkedAt, { locale: appLocale.value })
          }
        : null
    }))
);
</script>

<template>
  <section
    v-if="items.length"
    id="integrations"
    class="rounded-big border border-solid border-line-subtle bg-surface-card px-18px py-16px"
    aria-labelledby="integrations-title"
    data-testid="package-integrations"
  >
    <h2 id="integrations-title" class="m-0 text-13.5px font-600 text-ink-primary">
      {{ t('package.integrations.title', { name: pkg.displayName }) }}
    </h2>
    <p class="m-0 mt-4px text-12px leading-[1.6] text-ink-tertiary">{{ t('package.integrations.hint') }}</p>
    <article
      v-for="item in items"
      :key="item.key"
      class="mt-10px border-t border-t-solid border-line-subtle pt-10px first-of-type:border-t-0"
    >
      <h3 class="m-0 text-13px font-600 text-ink-primary">
        {{ item.name
        }}<span class="text-12px font-400 text-ink-tertiary">{{ ` · ${item.kind} · ${item.version}` }}</span>
      </h3>
      <dl
        class="m-0 mt-6px grid grid-cols-1 gap-x-16px gap-y-4px text-12.5px leading-[1.6] sm:grid-cols-[max-content_minmax(0,1fr)]"
      >
        <dt class="text-ink-tertiary">{{ t('package.integrations.install') }}</dt>
        <dd class="m-0 text-ink-primary">
          <code class="font-mono text-12px text-component-mono-text [overflow-wrap:anywhere]">{{ item.install }}</code>
        </dd>
        <dt class="text-ink-tertiary">{{ t('package.integrations.clients') }}</dt>
        <dd class="m-0 text-ink-primary">{{ item.clients }}</dd>
        <dt class="text-ink-tertiary">{{ t('package.integrations.files') }}</dt>
        <dd class="m-0 text-ink-primary">
          <template v-if="item.files.length">
            <span v-for="file in item.files" :key="file.path" class="block"
              ><code class="font-mono text-12px text-component-mono-text [overflow-wrap:anywhere]">{{ file.path }}</code
              >{{ ` · ${file.access}` }}</span
            >
          </template>
          <template v-else>{{ t('package.integrations.none') }}</template>
        </dd>
        <dt class="text-ink-tertiary">{{ t('package.integrations.network') }}</dt>
        <dd class="m-0 text-ink-primary [overflow-wrap:anywhere]">
          {{ item.network.length ? item.network.join(', ') : t('package.integrations.none') }}
        </dd>
        <template v-if="item.silicon">
          <dt class="text-ink-tertiary">{{ t('package.arch.arm64') }}</dt>
          <dd class="m-0 text-ink-primary">{{ item.silicon }}</dd>
        </template>
        <dt class="text-ink-tertiary">{{ t('package.details.repo') }}</dt>
        <dd class="m-0">
          <a
            :href="item.repository"
            target="_blank"
            rel="noopener noreferrer"
            class="text-ink-secondary underline-offset-2 [overflow-wrap:anywhere] hover:text-ink-primary"
            >{{ displayUrl(item.repository) }}</a
          >
        </dd>
      </dl>
      <details v-if="item.versions.length" class="mt-6px text-12.5px">
        <summary
          class="cursor-pointer text-ink-secondary outline-none hover:text-ink-primary focus-visible:shadow-focus-ring"
        >
          {{ t('package.integrations.history') }}
        </summary>
        <ul class="m-0 mt-4px list-none p-0">
          <li v-for="entry in item.versions" :key="entry.version" class="text-ink-secondary">
            <a
              :href="entry.url"
              target="_blank"
              rel="nofollow noopener noreferrer"
              class="font-mono text-12px text-ink-primary underline-offset-2 hover:underline"
              >{{ entry.version }}</a
            >
            · <time :datetime="entry.iso">{{ entry.date }}</time>
          </li>
        </ul>
      </details>
      <p class="m-0 mt-4px text-11.5px text-ink-tertiary">
        <a
          :href="item.source"
          target="_blank"
          rel="nofollow noopener noreferrer"
          class="text-ink-secondary underline-offset-2 hover:text-ink-primary"
          >{{ displayUrl(item.source) }}</a
        ><template v-if="item.checked">
          · <time :datetime="item.checked.iso">{{ item.checked.date }}</time></template
        >
      </p>
    </article>
  </section>
</template>
