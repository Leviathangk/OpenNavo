<script setup lang="ts">
import { computed } from 'vue';
import { useI18n } from 'vue-i18n';
import { formatRelativeTime } from '@opennavo/shared';
import { OnAppIcon, OnChip, OnIcon, OnMenu } from '@opennavo/ui';
import type { OnMenuItem } from '@opennavo/ui';
import type { FirewallRule } from '@/ipc/bindings';
import { useAppLocale } from '@/composables/useAppLocale';
import { appIcon, developerText, isBundle, ruleDestination, shortPath, SIGNER_TONE } from './meta';
import type { FirewallRuleGroup } from './meta';

// One program and its rules (firewall plan §5): its icon, who signed it and where it lives, then each rule — allow or
// block, where to, for how long, why it exists and when it last decided a connection.
const props = defineProps<{ group: FirewallRuleGroup; busy: ReadonlySet<string> }>();
const emit = defineEmits<{
  act: [rule: FirewallRule, action: 'allow' | 'block' | 'edit' | 'delete'];
}>();

const { t } = useI18n();
const { appLocale } = useAppLocale();

const app = computed(() => props.group.app);
const name = computed(() => app.value?.name ?? t('firewall.rules.allApps'));
const subtitle = computed(() => {
  const value = app.value;
  if (!value) return t('firewall.rules.allAppsHint');
  const path = shortPath(value.bundlePath ?? value.path, 72);
  // Other signers show as a chip next to the name; only a developer's name goes in front of the path.
  return value.signer === 'developer_id' ? `${developerText(value, t)} · ${path}` : path;
});
// The common case, one rule for any destination, fits on the app's own line.
const inline = computed(() => {
  const [rule, ...rest] = props.group.rules;
  return rule && !rest.length && rule.remote === null && rule.port === null ? rule : null;
});

// While a rule's change is in flight its menu stays, with every item disabled.
function menu(rule: FirewallRule): OnMenuItem[] {
  const disabled = props.busy.has(rule.id);
  return [
    rule.action === 'allow'
      ? { key: 'block', label: t('firewall.rules.menu.block'), icon: 'ban', disabled }
      : { key: 'allow', label: t('firewall.rules.menu.allow'), icon: 'check', disabled },
    { key: 'edit', label: t('firewall.rules.menu.edit'), icon: 'sliders-horizontal', disabled },
    { key: 'delete', label: t('firewall.rules.menu.delete'), icon: 'trash', danger: true, disabled }
  ];
}

const used = (rule: FirewallRule) =>
  rule.lastMatchAt === null
    ? t('firewall.rules.neverMatched')
    : t('firewall.rules.lastMatch', {
        time: formatRelativeTime(rule.lastMatchAt, { locale: appLocale.value }),
        count: rule.matchCount
      });
</script>

<template>
  <li class="border-t border-t-solid border-line-subtle first:border-t-0" :data-rule-group="group.key">
    <div class="flex items-center gap-12px px-14px py-10px">
      <OnAppIcon
        v-if="app && isBundle(app)"
        kind="cask"
        :token="app.bundleId ?? app.key"
        :name="app.name"
        :src="appIcon(app)"
        :size="32"
      />
      <OnAppIcon v-else-if="app" kind="formula" :token="app.name" :name="app.name" :size="32" />
      <span
        v-else
        class="flex h-32px w-32px shrink-0 items-center justify-center rounded-big bg-surface-inset text-ink-secondary"
      >
        <OnIcon name="globe" :size="17" />
      </span>
      <div class="min-w-0 flex-1">
        <p class="m-0 flex items-center gap-6px">
          <span class="min-w-0 truncate text-13px font-600 text-ink-primary" :title="app?.path">{{ name }}</span>
          <OnChip v-if="app && app.signer !== 'developer_id'" :tone="SIGNER_TONE[app.signer]" size="sm">{{
            t(`firewall.signer.${app.signer}`)
          }}</OnChip>
        </p>
        <p class="m-0 truncate text-11.5px text-ink-tertiary" :title="app?.bundlePath ?? app?.path">{{ subtitle }}</p>
      </div>
      <template v-if="inline">
        <span class="hidden w-150px shrink-0 truncate text-right text-11.5px text-ink-tertiary md:block">{{
          used(inline)
        }}</span>
        <span class="flex w-200px shrink-0 items-center justify-end gap-6px">
          <span v-if="inline.source !== 'user'" class="truncate text-11.5px text-ink-tertiary">{{
            t(`firewall.rules.source.${inline.source}`)
          }}</span>
          <OnChip v-if="inline.until === 'app_quits'" size="sm">{{ t('firewall.rules.untilQuit') }}</OnChip>
          <OnChip :tone="inline.action === 'allow' ? 'success' : 'danger'" size="sm">{{
            t(`firewall.rules.action.${inline.action}`)
          }}</OnChip>
          <OnMenu
            :items="menu(inline)"
            :label="t('firewall.rules.menuLabel', { name })"
            variant="ghost"
            size="xs"
            @select="key => emit('act', inline!, key as 'allow' | 'block' | 'edit' | 'delete')"
          />
        </span>
      </template>
    </div>
    <ul v-if="!inline" class="m-0 list-none p-0 pb-6px">
      <li
        v-for="rule in group.rules"
        :key="rule.id"
        class="flex items-center gap-10px py-5px pl-58px pr-14px"
        :data-rule="rule.id"
      >
        <OnChip :tone="rule.action === 'allow' ? 'success' : 'danger'" size="sm">{{
          t(`firewall.rules.action.${rule.action}`)
        }}</OnChip>
        <span class="min-w-0 flex-1 truncate font-mono text-12px text-ink-primary" :title="rule.remote ?? undefined">{{
          ruleDestination(rule, t)
        }}</span>
        <OnChip v-if="rule.until === 'app_quits'" size="sm">{{ t('firewall.rules.untilQuit') }}</OnChip>
        <span v-if="rule.source !== 'user'" class="shrink-0 text-11.5px text-ink-tertiary">{{
          t(`firewall.rules.source.${rule.source}`)
        }}</span>
        <span class="hidden w-150px shrink-0 truncate text-right text-11.5px text-ink-tertiary md:block">{{
          used(rule)
        }}</span>
        <OnMenu
          :items="menu(rule)"
          :label="t('firewall.rules.menuLabel', { name })"
          variant="ghost"
          size="xs"
          @select="key => emit('act', rule, key as 'allow' | 'block' | 'edit' | 'delete')"
        />
      </li>
    </ul>
  </li>
</template>
