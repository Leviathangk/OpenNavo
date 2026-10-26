<script setup lang="ts">
import { formatDate } from '~/utils/packageDate';
import { displayUrl, OnIcon } from '@opennavo/ui';
import { aroundDate, isoDate, isVerified, verifiedIdentities, verifiedPrices } from '~/utils/packageFacts';
import type { FactEvidence, PackagePage } from '~/utils/packageFacts';

// Security facts (F5): only what can be checked, each with its source and date: the developer and Team ID,
// where the app is distributed, whether its source is open and where, and known vulnerabilities, limited to
// CVEs NVD matches exactly to this product and version. Being listed, signed or scanned clean is never
// presented as a safety certification, and an empty CVE list is not shown as a verdict. The scan link opens
// a preview in the OpenNavo app; the person starts the scan there. Its note links to the page on ClamAV,
// the engine that scan runs on (F8).
const props = defineProps<{ pkg: PackagePage }>();

const { t, locale } = useI18n();
const localePath = useLocalePath();
const appLocale = computed(() => toAppLocale(locale.value));

const security = computed(() => props.pkg.facts.security);
const identities = computed(() => verifiedIdentities(props.pkg));

function checked(evidence: FactEvidence) {
  if (!evidence.checkedAt) return null;
  return {
    iso: isoDate(evidence.checkedAt),
    date: formatDate(evidence.checkedAt, { locale: appLocale.value }),
    ...aroundDate(t, 'package.facts.checkedOn')
  };
}

const identity = computed(() => identities.value.find(item => item.developer || item.teamId));
const identityChecked = computed(() => (identity.value ? checked(identity.value.evidence) : null));

// Channels a verified record names; without one, the ones the catalog itself shows (Homebrew for casks,
// the developer's download for link apps, a Mac App Store listing with a verified price).
const channels = computed(() => {
  const named = new Set(identities.value.flatMap(item => item.channels));
  if (!named.size) {
    if (props.pkg.kind === 'cask') named.add('homebrew');
    if (props.pkg.kind === 'link' && props.pkg.link?.downloadUrl) named.add('vendor');
    if (verifiedPrices(props.pkg).some(price => price.channel === 'app_store')) named.add('app_store');
  }
  return (['homebrew', 'vendor', 'app_store'] as const)
    .filter(channel => named.has(channel))
    .map(channel => (channel === 'homebrew' ? 'Homebrew' : t(`package.price.channels.${channel}`)));
});

const openSource = computed(() => {
  const item = identities.value.find(entry => entry.openSource !== 'unknown');
  if (!item) return null;
  const license = props.pkg.openSource?.url === item.repositoryUrl ? props.pkg.openSource?.license : undefined;
  return { open: item.openSource === 'yes', url: item.repositoryUrl, license, checked: checked(item.evidence) };
});

const vulnerabilities = computed(() =>
  (security.value?.vulnerabilities ?? []).filter(item => isVerified(item.evidence))
);
const visible = computed(() => Boolean(security.value) || channels.value.length > 0);
</script>

<template>
  <section
    v-if="visible"
    id="safety"
    class="rounded-big border border-solid border-line-subtle bg-surface-card px-18px py-16px"
    aria-labelledby="safety-title"
    data-testid="package-safety"
  >
    <h2 id="safety-title" class="m-0 text-13.5px font-600 text-ink-primary">
      {{ t('package.safety.title', { name: pkg.displayName }) }}
    </h2>
    <p class="m-0 mt-4px text-12px leading-[1.6] text-ink-tertiary">{{ t('package.safety.hint') }}</p>
    <dl class="m-0 mt-10px flex flex-col gap-10px text-13px leading-[1.6]">
      <div v-if="identity">
        <dt class="text-12px text-ink-tertiary">{{ t('package.safety.identity') }}</dt>
        <dd class="m-0 text-ink-primary">
          <template v-if="identity.developer">{{ identity.developer }}</template>
          <template v-if="identity.teamId"
            >{{ `${identity.developer ? ' · ' : ''}${t('package.safety.teamId')} `
            }}<code class="font-mono text-12px text-component-mono-text">{{ identity.teamId }}</code></template
          >
          <span class="mt-2px block text-12px text-ink-tertiary"
            ><a
              :href="identity.evidence.sourceUrl"
              target="_blank"
              rel="nofollow noopener noreferrer"
              class="text-ink-secondary underline-offset-2 hover:text-ink-primary"
              >{{ displayUrl(identity.evidence.sourceUrl) }}</a
            ><template v-if="identityChecked"
              >{{ ` · ${identityChecked.before}`
              }}<time :datetime="identityChecked.iso">{{ identityChecked.date }}</time
              >{{ identityChecked.after }}</template
            ></span
          >
        </dd>
      </div>
      <div v-if="channels.length">
        <dt class="text-12px text-ink-tertiary">{{ t('package.safety.channels') }}</dt>
        <dd class="m-0 text-ink-primary">{{ channels.join(' · ') }}</dd>
      </div>
      <div v-if="openSource">
        <dt class="text-12px text-ink-tertiary">{{ t('package.safety.openSource') }}</dt>
        <dd class="m-0 text-ink-primary">
          {{ t(openSource.open ? 'package.facts.yes' : 'package.facts.no') }}
          <template v-if="openSource.url">
            ·
            <a
              :href="openSource.url"
              target="_blank"
              rel="noopener noreferrer"
              class="text-ink-secondary underline-offset-2 hover:text-ink-primary"
              >{{ displayUrl(openSource.url) }}</a
            ></template
          ><template v-if="openSource.license">{{ ` · ${openSource.license}` }}</template>
          <span v-if="openSource.checked" class="mt-2px block text-12px text-ink-tertiary"
            >{{ openSource.checked.before }}<time :datetime="openSource.checked.iso">{{ openSource.checked.date }}</time
            >{{ openSource.checked.after }}</span
          >
        </dd>
      </div>
      <div v-if="vulnerabilities.length">
        <dt class="text-12px text-ink-tertiary">{{ t('package.safety.cves') }}</dt>
        <dd v-for="item in vulnerabilities" :key="item.id" class="m-0 text-ink-primary">
          <a
            :href="item.sourceUrl"
            target="_blank"
            rel="nofollow noopener noreferrer"
            class="font-mono text-12.5px text-ink-primary underline-offset-2 hover:underline"
            >{{ item.id }}</a
          >{{ ` · ${t('package.safety.cveVersion', { version: item.appVersion })}` }}
        </dd>
        <dd class="m-0 mt-2px text-12px text-ink-tertiary">{{ t('package.safety.cveNote') }}</dd>
      </div>
    </dl>
    <div v-if="security?.scanUrl" class="mt-12px flex flex-wrap items-center gap-x-12px gap-y-6px">
      <a
        :href="security.scanUrl"
        rel="nofollow"
        class="inline-flex items-center gap-6px rounded-default border border-solid border-line-default px-12px py-6px text-12.5px font-600 text-ink-primary no-underline outline-none hover:border-line-strong focus-visible:shadow-focus-ring"
      >
        <OnIcon name="shield-check" :size="14" />
        {{ t('package.safety.scan') }}
      </a>
      <span class="text-12px text-ink-tertiary"
        >{{ `${t('package.safety.scanNote')} `
        }}<NuxtLink
          :to="localePath('/security/clamav')"
          class="text-ink-secondary underline-offset-2 hover:text-ink-primary"
          >{{ t('securityTopics.links.clamav.title') }}</NuxtLink
        ></span
      >
    </div>
  </section>
</template>
