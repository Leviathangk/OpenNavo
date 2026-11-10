<script setup lang="ts">
import { computed, ref } from 'vue';
import { RouterLink } from 'vue-router';
import { OnAppIcon, OnButton, OnChip, OnGetButton, OnIcon, OnModal } from '@opennavo/ui';
import { OnMarkdown } from '@opennavo/ui/markdown';
import { formatCount } from '@opennavo/shared';
import HermesMark from './HermesMark.vue';
import { useHermesContent } from '@/composables/useHermesContent';
import { useAppLocale } from '@/composables/useAppLocale';
import { usePackageState } from '@/composables/usePackageState';
import { useToasts } from '@/composables/useToasts';
import { useHermesStore } from '@/stores/hermes';
import { useTasksStore } from '@/stores';
import type { HermesMessage } from '@/stores/hermes';

const props = defineProps<{ message: HermesMessage }>();

const content = useHermesContent();
const hermes = useHermesStore();
const tasks = useTasksStore();
const pkgState = usePackageState();
const toasts = useToasts();
const { appLocale } = useAppLocale();

const confirmOpen = ref(false);
const demoCards = import.meta.env.MODE === 'web';
const pkg = computed(() => props.message.package ?? null);
const packageState = computed(() =>
  pkg.value ? pkgState.stateOf(pkg.value.kind, pkg.value.token, pkg.value.disabled) : null
);
function messageText(key?: string): string {
  return key ? ((content.value.messages as Record<string, string>)[key.replace(/^messages\./, '')] ?? key) : '';
}

function toolText(key: string): string {
  return (content.value.tools as Record<string, string>)[key] ?? key;
}

const text = computed(() => messageText(props.message.textKey) || props.message.text || '');
const attachments = computed(() =>
  (props.message.attachments ?? []).map(path => ({
    path,
    name: path.split('/').filter(Boolean).at(-1) ?? path,
    image: /\.(png|jpe?g|gif|webp|heic|heif|bmp|tiff)$/i.test(path)
  }))
);
const foot = computed(() => messageText(props.message.footKey));
const time = computed(() =>
  new Date(props.message.at).toLocaleTimeString(appLocale.value, { hour: '2-digit', minute: '2-digit' })
);
const toolCount = computed(() =>
  content.value.tools.calls.replace('{count}', String(props.message.tools?.length ?? 0))
);
const packageNote = computed(() => {
  if (!packageState.value) return '';
  if (packageState.value.state === 'running') return content.value.cards.package.installing;
  if (packageState.value.state === 'open' || packageState.value.state === 'installed')
    return content.value.cards.package.doneNote;
  if (hermes.mac.confirmWrites) return content.value.cards.package.confirmNote;
  return content.value.cards.package.note;
});

const updates = [
  { token: 'codex', name: 'Codex', version: '0.167.0', accent: '#111111' },
  { token: 'claude-code', name: 'Claude Code', version: '2.1.311', accent: '#DA7757' },
  { token: 'figma', name: 'Figma', version: '126.11.8', accent: '#2DCABD' },
  { token: 'raycast', name: 'Raycast', version: '2.11.1', accent: '#7B4646' }
];

function runPackageAction() {
  if (!pkg.value || !packageState.value) return;
  void pkgState.act(pkg.value.kind, pkg.value.token, packageState.value.state);
}

function onPackageAction() {
  if (!pkg.value || !packageState.value) return;
  if (!hermes.mac.enabled) {
    toasts.push({
      tone: 'warning',
      title: content.value.common.macPaused,
      description: content.value.common.macPausedBody
    });
    return;
  }
  if (hermes.mac.confirmWrites) {
    confirmOpen.value = true;
    return;
  }
  runPackageAction();
}

function onPackageCancel() {
  if (!pkg.value) return;
  const task = tasks.forPackage(pkg.value.kind, pkg.value.token);
  if (task) void tasks.cancel(task.id);
}
</script>

<template>
  <div v-if="message.role === 'user'" class="flex justify-end">
    <div
      class="max-w-[82%] rounded-[14px_14px_4px_14px] border border-solid px-14px py-10px text-13.5px leading-[1.65] text-ink-primary"
      :class="
        message.steering
          ? 'border-[rgba(250,178,25,0.22)] bg-[rgba(250,178,25,0.06)]'
          : 'border-line-subtle bg-surface-raised'
      "
    >
      <div v-if="message.steering" class="mb-5px flex items-center gap-5px text-10px font-500 text-status-warning/80">
        <OnIcon name="lucide:corner-down-right" :size="10" />
        {{ content.steering }}
      </div>
      <div v-if="attachments.length" class="mb-7px flex flex-wrap gap-5px">
        <span
          v-for="file in attachments"
          :key="file.path"
          class="inline-flex max-w-full items-center gap-4px rounded-tiny bg-surface-control px-7px py-4px text-11px text-ink-secondary"
        >
          <OnIcon
            :name="file.image ? 'lucide:image' : 'lucide:paperclip'"
            :size="11"
            class="shrink-0 text-ink-tertiary"
          />
          <span class="min-w-0 truncate">{{ file.name }}</span>
        </span>
      </div>
      <div v-if="text" class="whitespace-pre-wrap">{{ text }}</div>
    </div>
  </div>

  <div v-else class="flex items-start gap-12px">
    <HermesMark :size="26" class="mt-1px" />
    <div class="flex min-w-0 flex-1 flex-col gap-12px">
      <div class="flex items-center gap-8px text-11px text-ink-tertiary">
        <b class="text-12.5px font-600 text-ink-primary">Hermes</b>
        <span>{{ time }}</span>
      </div>

      <details
        v-if="message.reasoning"
        class="group overflow-hidden rounded-small border border-solid border-line-subtle bg-surface-card"
      >
        <summary
          class="flex h-32px cursor-pointer list-none items-center gap-8px px-10px text-11px text-ink-tertiary outline-none focus-visible:shadow-focus-ring"
        >
          <OnIcon name="lucide:brain" :size="13" />
          {{ content.composer.thinking }}
          <OnIcon name="lucide:chevron-down" :size="13" class="ml-auto transition-transform group-open:rotate-180" />
        </summary>
        <pre
          class="m-0 max-h-220px overflow-auto whitespace-pre-wrap break-words border-t border-t-solid border-line-subtle px-10px py-8px font-sans text-11px leading-[1.55] text-ink-tertiary"
          >{{ message.reasoning }}</pre
        >
      </details>

      <details
        v-if="message.tools?.length"
        class="group overflow-hidden rounded-small border border-solid border-line-subtle bg-surface-card"
      >
        <summary
          class="flex h-32px cursor-pointer list-none items-center gap-8px px-10px text-11px text-ink-tertiary outline-none focus-visible:shadow-focus-ring"
        >
          <OnIcon name="lucide:square-terminal" :size="13" />
          {{ toolCount }}
          <OnIcon name="lucide:chevron-down" :size="13" class="ml-auto transition-transform group-open:rotate-180" />
        </summary>
        <div class="flex flex-col gap-7px border-t border-t-solid border-line-subtle px-10px pb-9px pt-8px">
          <div
            v-for="call in message.tools"
            :key="call.name"
            class="flex items-center gap-8px text-11px text-ink-tertiary"
          >
            <code class="rounded-tiny bg-surface-inset px-5px py-2px font-mono text-11px text-ink-secondary">{{
              call.name
            }}</code>
            <span>{{ toolText(call.label) }}</span>
            <span class="ml-auto tabular-nums">{{ call.time }}</span>
            <OnIcon name="lucide:check" :size="12" class="text-status-success" />
          </div>
        </div>
      </details>

      <div class="min-w-0" aria-live="polite">
        <div v-if="message.streaming && !text" class="flex items-center gap-8px pt-1px text-11px text-ink-tertiary">
          <span class="h-5px w-5px animate-pulse rounded-full bg-brand-coral"></span>
          {{ content.composer.thinking }}
        </div>
        <OnMarkdown v-else-if="text" :source="text" size="sm" :heading-level="4" />
        <span
          v-if="message.streaming && text"
          class="ml-2px inline-block h-13px w-7px animate-pulse rounded-2px bg-ink-secondary align-[-2px]"
        />
      </div>

      <div
        v-if="demoCards && pkg && packageState"
        class="flex flex-col gap-12px rounded-big border border-solid border-line-subtle bg-surface-card p-14px"
      >
        <div class="flex items-start gap-12px">
          <OnAppIcon
            :kind="pkg.kind"
            :token="pkg.token"
            :name="pkg.displayName"
            :src="pkg.iconUrl"
            :accent="pkg.accentColor"
            :size="44"
          />
          <div class="min-w-0 flex-1">
            <div class="flex items-center gap-7px">
              <b class="text-15px font-600 tracking-[-0.01em] text-ink-primary">{{ pkg.displayName }}</b>
              <OnChip tone="success">{{ content.cards.package.free }}</OnChip>
            </div>
            <p v-if="pkg.summary" class="m-0 mt-3px text-12px leading-[1.55] text-ink-tertiary">
              {{ content.cards.package.summary }}
            </p>
          </div>
        </div>
        <div class="flex gap-22px border-t border-t-solid border-line-subtle pt-12px">
          <div>
            <span class="block text-11px text-ink-tertiary">{{ content.cards.package.version }}</span>
            <span class="mt-2px block text-12.5px font-600 tabular-nums text-ink-primary">{{ pkg.version }}</span>
          </div>
          <div>
            <span class="block text-11px text-ink-tertiary">{{ content.cards.package.installs }}</span>
            <span class="mt-2px block text-12.5px font-600 tabular-nums text-ink-primary">{{
              formatCount(pkg.installs30d, { locale: appLocale })
            }}</span>
          </div>
          <div>
            <span class="block text-11px text-ink-tertiary">{{ content.cards.package.category }}</span>
            <span class="mt-2px block text-12.5px font-600 text-ink-primary">{{
              content.cards.package.categoryValue
            }}</span>
          </div>
        </div>
        <div class="flex items-center gap-8px">
          <span class="inline-flex min-w-0 items-center gap-6px text-11px leading-[1.4] text-ink-tertiary">
            <OnIcon name="lucide:shield-check" :size="12" />
            {{ packageNote }}
          </span>
          <div class="ml-auto flex items-center gap-8px">
            <RouterLink
              :to="`/package/${pkg.kind}/${pkg.token}`"
              class="inline-flex h-30px items-center rounded-small border border-solid border-line-default px-12px text-12px font-600 text-ink-secondary no-underline outline-none transition-colors hover:border-line-strong hover:text-ink-primary focus-visible:shadow-focus-ring"
            >
              {{ content.cards.package.details }}
            </RouterLink>
            <OnGetButton
              :state="packageState.state"
              :progress="packageState.progress"
              appearance="button"
              @click="onPackageAction"
              @cancel="onPackageCancel"
            />
          </div>
        </div>
      </div>

      <div
        v-if="demoCards && message.card === 'cleanup'"
        class="flex flex-col gap-12px rounded-big border border-solid border-line-subtle bg-surface-card p-14px"
      >
        <div class="flex items-start gap-12px">
          <div>
            <span class="block text-11px text-ink-tertiary">{{ content.cards.cleanup.kicker }}</span>
            <b class="mt-2px block text-24px leading-[1.1] tracking-[-0.025em] text-ink-primary">{{
              content.cards.cleanup.size
            }}</b>
          </div>
          <OnChip tone="accent" class="ml-auto">{{ content.cards.cleanup.needConfirm }}</OnChip>
        </div>
        <div class="flex h-8px gap-2px overflow-hidden rounded-full bg-surface-inset">
          <i class="block h-full w-[55%] bg-status-info"></i>
          <i class="block h-full w-[25%] bg-brand-salmon"></i>
          <i class="block h-full w-[20%] bg-brand-coral"></i>
        </div>
        <div class="flex flex-col gap-8px">
          <div class="flex items-center gap-8px text-11.5px text-ink-secondary">
            <i class="h-8px w-8px rounded-[2px] bg-status-info"></i>{{ content.cards.cleanup.caches
            }}<span class="ml-auto font-600 tabular-nums text-ink-primary">{{ content.cards.cleanup.cachesSize }}</span>
          </div>
          <div class="flex items-center gap-8px text-11.5px text-ink-secondary">
            <i class="h-8px w-8px rounded-[2px] bg-brand-salmon"></i>{{ content.cards.cleanup.leftovers
            }}<span class="ml-auto font-600 tabular-nums text-ink-primary">{{
              content.cards.cleanup.leftoversSize
            }}</span>
          </div>
          <div class="flex items-center gap-8px text-11.5px text-ink-secondary">
            <i class="h-8px w-8px rounded-[2px] bg-brand-coral"></i>{{ content.cards.cleanup.installers
            }}<span class="ml-auto font-600 tabular-nums text-ink-primary">{{
              content.cards.cleanup.installersSize
            }}</span>
          </div>
        </div>
        <div class="flex items-center gap-8px">
          <span class="inline-flex items-center gap-6px text-11px text-ink-tertiary"
            ><OnIcon name="lucide:trash" :size="12" />{{ content.cards.cleanup.note }}</span
          >
          <div class="ml-auto flex items-center gap-8px">
            <RouterLink
              to="/cleanup"
              class="inline-flex h-30px items-center rounded-small border border-solid border-line-default px-12px text-12px font-600 text-ink-secondary no-underline outline-none hover:border-line-strong hover:text-ink-primary focus-visible:shadow-focus-ring"
              >{{ content.cards.cleanup.review }}</RouterLink
            >
            <RouterLink
              to="/cleanup"
              class="inline-flex h-30px items-center rounded-small bg-button-accent-bg px-12px text-12px font-600 text-button-accent-text no-underline outline-none hover:bg-button-accent-hover focus-visible:shadow-focus-ring"
              >{{ content.cards.cleanup.run }}</RouterLink
            >
          </div>
        </div>
      </div>

      <div
        v-if="demoCards && message.card === 'updates'"
        class="flex flex-col gap-12px rounded-big border border-solid border-line-subtle bg-surface-card p-14px"
      >
        <div class="flex items-start gap-12px">
          <div>
            <span class="block text-11px text-ink-tertiary">{{ content.cards.updates.kicker }}</span>
            <b class="mt-2px block text-15px font-600 text-ink-primary">{{ content.cards.updates.total }}</b>
          </div>
          <OnChip tone="accent" class="ml-auto">{{ updates.length }}</OnChip>
        </div>
        <div class="flex flex-col">
          <div
            v-for="item in updates"
            :key="item.token"
            class="flex items-center gap-10px border-b border-b-solid border-line-subtle py-9px last:border-b-0"
          >
            <OnAppIcon kind="cask" :token="item.token" :name="item.name" :src="null" :accent="item.accent" :size="30" />
            <span class="min-w-0 flex-1"
              ><b class="block text-12.5px font-600 text-ink-primary">{{ item.name }}</b
              ><span class="mt-1px block font-mono text-11px text-ink-tertiary">{{ item.version }}</span></span
            >
          </div>
        </div>
        <div class="flex items-center gap-8px">
          <span class="inline-flex items-center gap-6px text-11px text-ink-tertiary"
            ><OnIcon name="lucide:info" :size="12" />{{ content.cards.updates.note }}</span
          >
          <div class="ml-auto flex items-center gap-8px">
            <RouterLink
              to="/updates"
              class="inline-flex h-30px items-center rounded-small border border-solid border-line-default px-12px text-12px font-600 text-ink-secondary no-underline outline-none hover:border-line-strong hover:text-ink-primary focus-visible:shadow-focus-ring"
              >{{ content.cards.updates.review }}</RouterLink
            >
            <RouterLink
              to="/updates"
              class="inline-flex h-30px items-center rounded-small bg-button-accent-bg px-12px text-12px font-600 text-button-accent-text no-underline outline-none hover:bg-button-accent-hover focus-visible:shadow-focus-ring"
              >{{ content.cards.updates.run }}</RouterLink
            >
          </div>
        </div>
      </div>

      <div
        v-if="demoCards && message.card === 'explain'"
        class="flex flex-col gap-12px rounded-big border border-solid border-line-subtle bg-surface-card p-14px"
      >
        <div class="flex items-start gap-12px">
          <OnAppIcon kind="cask" token="opennavo" name="OpenNavo" :src="null" accent="#FF7356" :size="40" />
          <div class="min-w-0 flex-1">
            <div class="flex items-center gap-7px">
              <b class="text-15px font-600 text-ink-primary">{{ content.cards.explain.title }}</b
              ><OnChip tone="neutral">{{ content.cards.explain.current }}</OnChip>
            </div>
            <p class="m-0 mt-3px text-12px leading-[1.55] text-ink-tertiary">{{ content.cards.explain.body }}</p>
          </div>
        </div>
        <div class="flex items-center gap-8px">
          <span class="text-11px text-ink-tertiary">{{ content.cards.explain.noCopy }}</span>
          <RouterLink
            to="/discover"
            class="ml-auto inline-flex h-30px items-center rounded-small border border-solid border-line-default px-12px text-12px font-600 text-ink-secondary no-underline outline-none hover:border-line-strong hover:text-ink-primary focus-visible:shadow-focus-ring"
            >{{ content.cards.explain.back }}</RouterLink
          >
        </div>
      </div>

      <p v-if="foot" class="m-0 text-11px leading-[1.5] text-ink-tertiary">{{ foot }}</p>
    </div>
  </div>

  <OnModal
    :open="confirmOpen"
    :title="content.cards.package.confirmTitle"
    :description="content.cards.package.confirmBody"
    size="md"
    role="alertdialog"
    initial-focus="[data-hermes-install-cancel]"
    @close="confirmOpen = false"
  >
    <div
      class="flex items-center gap-8px rounded-small border border-solid border-line-subtle bg-surface-inset px-12px py-10px font-mono text-11.5px text-ink-secondary"
    >
      <OnIcon name="lucide:square-terminal" :size="14" />
      {{ content.cards.package.command }}
    </div>
    <ul class="m-0 mt-12px flex list-none flex-col gap-7px p-0 text-11.5px leading-[1.5] text-ink-tertiary">
      <li class="flex items-start gap-7px">
        <OnIcon name="lucide:shield-check" :size="12" class="mt-1px" />{{ content.cards.package.modalNote }}
      </li>
    </ul>
    <template #footer>
      <OnButton variant="ghost" data-hermes-install-cancel @click="confirmOpen = false">{{
        content.common.cancel
      }}</OnButton>
      <OnButton
        variant="accent"
        @click="
          runPackageAction();
          confirmOpen = false;
        "
        >{{ content.cards.package.confirmRun }}</OnButton
      >
    </template>
  </OnModal>
</template>
