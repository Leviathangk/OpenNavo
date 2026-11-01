<script setup lang="ts">
import { packageInstallState } from '@opennavo/shared';
import {
  OnButton,
  OnIcon,
  OnPackageHeader,
  OnPackageStarBadge,
  toPackageRepoSignals,
  toPackageStarBadge,
  useUiMessages
} from '@opennavo/ui';
import type { OnPackageHeaderChip } from '@opennavo/ui';
import { trackEvent } from '~/utils/analytics';
import { httpsUrl, linkDownloadUrl, linkSource } from '~/utils/linkApp';
import { officialSource } from '~/utils/packageFacts';
import type { PackagePage } from '~/utils/packageFacts';
import { lifecycleReason } from '~/utils/packageInstall';
import PackageSourceBadge from './PackageSourceBadge.vue';
import { useToasts } from '~/composables/useToasts';

// Detail header (08 §10.5, 03-detail): shared OnPackageHeader (ADR-017), web Get menu/homepage right.
// Link apps (store/security plan §4) swap the Get menu for Download, which opens the developer's own address.
// T1: the categories are real links, and an app OpenNavo paused (no new version for 90 days, still offered by
// Homebrew) reads "paused", never "disabled", which only Homebrew's own disable earns.
const props = defineProps<{ pkg: PackagePage; headingSuffix?: string }>();

const { t, locale } = useI18n();
const messages = useUiMessages();
const localePath = useLocalePath();
const NuxtLink = resolveComponent('NuxtLink');

const isLink = computed(() => props.pkg.kind === 'link');
const link = computed(() => (isLink.value ? (props.pkg.link ?? null) : null));
const downloadUrl = computed(() => (link.value ? linkDownloadUrl(link.value) : undefined));
const homepage = computed(() =>
  isLink.value ? httpsUrl(link.value?.homepage ?? props.pkg.homepage) : props.pkg.homepage
);
const meta = computed(() => {
  if (!isLink.value) return '';
  const source = link.value ? linkSource(link.value) : undefined;
  return source ? t('package.link.meta', { source }) : t('package.link.notHosted');
});

const subtitle = computed(() => [props.pkg.developer, props.pkg.summary].filter(Boolean).join(' · '));
const repoSignals = computed(() => toPackageRepoSignals(props.pkg));
// GitHub star chip right of the source badge; hidden without a GitHub repository or a star count.
const starBadge = computed(() => toPackageStarBadge(props.pkg, toAppLocale(locale.value)));
const starAria = computed(() =>
  starBadge.value
    ? t('package.repo.starsBadge', {
        count: new Intl.NumberFormat(toAppLocale(locale.value)).format(starBadge.value.stars)
      })
    : ''
);
const installState = computed(() => packageInstallState(props.pkg));
const unavailable = computed(() => installState.value === 'disabled');
const source = computed(() => officialSource(props.pkg));
// Mobile install guidance: a disabled package explains why instead of offering the command; a deprecated or
// paused one still offers Homebrew's command with the warning first.
const installNotice = computed(() => {
  const state = installState.value;
  if (state === 'active' || isLink.value) return undefined;
  const reason = lifecycleReason(props.pkg, state, t);
  return {
    text: t(
      state === 'disabled'
        ? 'package.disabledNotice'
        : state === 'deprecated'
          ? 'package.deprecatedNotice'
          : 'package.staleNotice',
      { reason }
    ),
    disabled: state === 'disabled'
  };
});
// Pair actions in equal columns only when the header actually has Get/Download and Homepage.
const actionCount = computed(() => {
  if (isLink.value) return Number(Boolean(downloadUrl.value)) + Number(Boolean(homepage.value));
  return 1 + Number(Boolean(homepage.value));
});
const stretchActions = computed(() => actionCount.value === 2);

function onCopyFailed() {
  useToasts().push({ tone: 'warning', title: t('toast.copyFailed'), duration: 0 });
}

const chips = computed<OnPackageHeaderChip[]>(() => {
  const kind = isLink.value
    ? t(link.value?.downloadUrl || !link.value?.github ? 'package.link.chip' : 'package.link.chipGithub')
    : props.pkg.kind === 'cask'
      ? 'Cask'
      : 'Formula';
  const list: OnPackageHeaderChip[] = [{ key: 'kind', label: kind }];
  // The primary category first, then the others, each linking to its category page.
  const primary = props.pkg.primaryCategory;
  const categories = [
    ...(primary ? [primary] : []),
    ...(props.pkg.categories ?? []).filter(item => item.slug !== primary?.slug)
  ];
  for (const category of categories)
    list.push({
      key: `category:${category.slug}`,
      label: category.name,
      href: localePath(`/categories/${category.slug}`)
    });
  if (link.value && link.value.pricing !== 'unknown')
    list.push({ key: 'pricing', label: t(`package.link.pricing.${link.value.pricing}`) });
  if (props.pkg.autoUpdates) list.push({ key: 'auto', label: t('package.autoUpdates'), tone: 'info' });
  if (repoSignals.value?.archived)
    list.push({ key: 'archived', label: t('package.repo.archived'), tone: 'danger', outline: true });
  if (installState.value !== 'active' && !source.value)
    list.push({
      key: installState.value,
      label: t(`package.${installState.value}`),
      tone: unavailable.value ? 'danger' : 'warning',
      outline: true
    });
  return list;
});
</script>

<template>
  <OnPackageHeader
    :kind="pkg.kind"
    :token="pkg.token"
    :name="pkg.displayName"
    :heading-suffix="headingSuffix"
    :subtitle="subtitle"
    :icon-url="pkg.iconUrl"
    :icon-variants="pkg.iconVariants"
    :accent="pkg.accentColor"
    :chips="chips"
    :meta="meta"
    :link-as="NuxtLink"
    :stretch-actions="stretchActions"
    priority
  >
    <template #tags>
      <PackageSourceBadge v-if="source" :pkg="pkg" />
      <OnPackageStarBadge
        v-if="starBadge"
        :label="starBadge.label"
        :accessible-label="starAria"
        :href="starBadge.href"
      />
    </template>
    <template #actions>
      <template v-if="isLink">
        <OnButton
          v-if="downloadUrl"
          variant="primary"
          shape="round"
          :href="downloadUrl"
          target="_blank"
          rel="noopener noreferrer"
          @click="trackEvent('get_app')"
        >
          {{ t('package.link.goDownload') }}
          <OnIcon name="arrow-up-right" :size="15" />
        </OnButton>
      </template>
      <OnButton
        v-else-if="unavailable"
        :aria-label="t('get.label', { name: pkg.displayName })"
        variant="primary"
        shape="round"
        icon="download"
        disabled
      >
        {{ messages.getButton.get }}
      </OnButton>
      <GetMenu
        v-else
        variant="primary"
        block
        :kind="pkg.kind"
        :token="pkg.token"
        :name="pkg.displayName"
        :command="pkg.installCommand"
      />
      <OnButton v-if="homepage" shape="round" :href="homepage" target="_blank" rel="noopener noreferrer">
        {{ t('package.homepage') }}
        <OnIcon name="arrow-up-right" :size="15" />
      </OnButton>
    </template>
    <template #command>
      <!-- The install card: one command at every width, or a lifecycle warning (and no command when disabled). -->
      <div
        v-if="!isLink && (installNotice || !unavailable)"
        class="on-detail-install-command flex flex-col gap-10px basis-full col-span-2"
        data-testid="package-install-command"
      >
        <p
          v-if="installNotice"
          role="note"
          data-testid="package-mobile-install-notice"
          class="m-0 rounded-default border border-solid px-10px py-8px text-12px leading-[1.6]"
          :class="
            installNotice.disabled
              ? 'border-status-danger text-status-danger'
              : 'border-status-warning text-status-warning'
          "
        >
          {{ installNotice.text }}
        </p>
        <PackageCommand
          v-if="!unavailable"
          :command="pkg.installCommand"
          :copy-label="t('package.copy.install')"
          @copied="trackEvent('copy_install_command')"
          @copy-failed="onCopyFailed"
        />
      </div>
    </template>
  </OnPackageHeader>
</template>
