import { computed, ref, watch } from 'vue';
import { defineStore } from 'pinia';
import { usePermissionGateStore } from './permissionGate';
import type {
  CategoryReport,
  CleanupCategory,
  CleanupGroup,
  CleanupItem,
  CleanupProgress,
  CleanupReport,
  CleanupSummary,
  ProtectRule,
  SkippedGroupReason
} from '@/ipc/bindings';
import { IpcError, commands, unwrap } from '@/ipc/client';
import { usePermissionsStore } from './permissions';
import { useTasksStore } from './tasks';

/**
 * Clean Up Mac (cleanup proposal §06): scan → finishing (progress reaches 100%) → review → submitting → running → done.
 * Like uninstalling, the UI sends reportId + itemIds and never paths (§07).
 */
export type CleanupStage = 'idle' | 'scanning' | 'finishing' | 'review' | 'submitting' | 'running' | 'done' | 'failed';

/** Risk tiers in display order (§05). */
export const RISKS = ['rebuildable', 'review', 'personal'] as const;
/** Category order of the report (§07). */
export const CATEGORIES: readonly CleanupCategory[] = [
  'app_caches',
  'logs',
  'browser_caches',
  'dev_caches',
  'homebrew',
  'temp',
  'leftovers',
  'installers',
  'project_artifacts',
  'docker',
  'trash',
  'device_backups',
  'large_files'
];

/** Skip reasons that mean "running right now": measured and shown, never selectable; quitting frees them. */
export const IN_USE: readonly SkippedGroupReason[] = ['running', 'in_use', 'busy'];
/** Space of a category held by running apps and tools. */
export const inUseBytes = (category: CategoryReport) =>
  category.skipped.filter(group => IN_USE.includes(group.reason)).reduce((sum, group) => sum + (group.bytes ?? 0), 0);
/** Expansion key of a group: Rust's group ids (bundle ids, folder keys) repeat across categories. */
export const groupKey = (category: CleanupCategory, group: Pick<CleanupGroup, 'id'>) => `${category}/${group.id}`;

export interface CleanupEntry {
  category: CategoryReport;
  group: CleanupGroup;
  item: CleanupItem;
}

const sizeOf = (items: readonly CleanupItem[]) => items.reduce((sum, item) => sum + (item.sizeBytes ?? 0), 0);
/** brew cleanup and direct deletes free space now; Trash moves only once the Trash is emptied. */
export const freesNow = (item: CleanupItem) => item.kind === 'command' || item.deleteMode === 'delete';
const failure = (cause: unknown) =>
  cause instanceof IpcError ? cause : new IpcError({ code: 'E_UNKNOWN', message: String(cause), detail: null });

function withoutItem(report: CleanupReport, itemId: string): CleanupReport {
  return {
    ...report,
    categories: report.categories
      .map(category => ({
        ...category,
        groups: category.groups
          .map(group => ({ ...group, items: group.items.filter(item => item.id !== itemId) }))
          .filter(group => group.items.length)
      }))
      .filter(category => category.groups.length || category.skipped.length)
  };
}

export const useCleanupStore = defineStore('cleanup', () => {
  const tasks = useTasksStore();
  const permissions = usePermissionsStore();

  const stage = ref<CleanupStage>('idle');
  const requestId = ref<string | null>(null);
  const progress = ref<CleanupProgress | null>(null);
  const report = ref<CleanupReport | null>(null);
  const pending = ref<CleanupReport | null>(null);
  /** itemId → checked. */
  const selected = ref<Record<string, boolean>>({});
  /** Expanded category keys, plus `groupKey` values of expanded groups; groups start folded. */
  const expanded = ref<string[]>([]);
  const summary = ref<CleanupSummary | null>(null);
  const rules = ref<ProtectRule[]>([]);
  const taskId = ref<string | null>(null);
  const error = ref<IpcError | null>(null);
  /** Shown once after an expired report was replaced. */
  const rescanned = ref(false);
  /** Free space when the run started, for the before / after line. */
  const freeBefore = ref<number | null>(null);
  const restored = ref(false);

  const categories = computed(() => report.value?.categories ?? []);
  /** Rust reads the disk live for every summary; the report's copy is from scan time. */
  const volume = computed(() => summary.value?.volume ?? report.value?.volume ?? null);
  const scanned = computed(() => summary.value?.scannedAt != null);
  const entries = computed<CleanupEntry[]>(() =>
    categories.value.flatMap(category =>
      category.groups.flatMap(group => group.items.map(item => ({ category, group, item })))
    )
  );
  const isChecked = (item: CleanupItem) => Boolean(selected.value[item.id]);
  const chosen = computed(() => entries.value.filter(entry => isChecked(entry.item)));
  const totals = computed(() => {
    const items = chosen.value.map(entry => entry.item);
    const bytes = sizeOf(items);
    const nowBytes = sizeOf(items.filter(freesNow));
    return {
      reclaimable: sizeOf(entries.value.filter(entry => entry.category.risk !== 'personal').map(entry => entry.item)),
      count: items.length,
      bytes,
      nowBytes,
      trashBytes: bytes - nowBytes,
      admin: items.filter(item => item.needsAdmin).length,
      emptiesTrash: chosen.value.some(entry => entry.category.key === 'trash')
    };
  });
  /** Space held by running apps and tools: not reclaimable now, shown so quitting them is worth knowing about. */
  const inUse = computed(() => categories.value.reduce((sum, category) => sum + inUseBytes(category), 0));
  /** Background items of deleted apps that still load at login. */
  const activeServices = computed(() => entries.value.filter(entry => entry.item.reason === 'orphan_service'));
  const task = computed(() =>
    taskId.value
      ? (tasks.active.find(item => item.id === taskId.value) ?? tasks.finished.find(item => item.id === taskId.value))
      : undefined
  );
  const outcome = computed(() => task.value?.outcome ?? null);
  /** freedBytes counts everything removed; the Trash part frees space only once the Trash is emptied. */
  const trashedBytes = computed(() => outcome.value?.trashedBytes ?? 0);
  const freedNow = computed(() => Math.max(0, (outcome.value?.freedBytes ?? 0) - trashedBytes.value));

  const itemsOf = (category: CategoryReport) => category.groups.flatMap(group => group.items);
  function stateOf(items: readonly CleanupItem[]): 'on' | 'ind' | 'off' {
    const count = items.filter(isChecked).length;
    return count === 0 ? 'off' : count === items.length ? 'on' : 'ind';
  }
  const categoryState = (category: CategoryReport) => stateOf(itemsOf(category));
  const groupState = (group: CleanupGroup) => stateOf(group.items);
  const setItem = (id: string, value: boolean) => {
    selected.value = { ...selected.value, [id]: value };
  };
  function setItems(items: readonly CleanupItem[], value: boolean) {
    const next = { ...selected.value };
    for (const item of items) next[item.id] = value;
    selected.value = next;
  }
  const setCategory = (category: CategoryReport, value: boolean) => setItems(itemsOf(category), value);
  const setGroup = (group: CleanupGroup, value: boolean) => setItems(group.items, value);
  function recommended() {
    selected.value = Object.fromEntries(entries.value.map(entry => [entry.item.id, entry.item.selected]));
  }
  function onlyRebuildable() {
    selected.value = Object.fromEntries(
      entries.value.map(entry => [entry.item.id, entry.category.risk === 'rebuildable'])
    );
  }
  function toggleExpanded(key: string) {
    expanded.value = expanded.value.includes(key)
      ? expanded.value.filter(value => value !== key)
      : [...expanded.value, key];
  }

  async function loadSummary() {
    summary.value = await unwrap(commands.cleanupSummary());
  }
  async function loadRules() {
    rules.value = await unwrap(commands.cleanupProtectedList());
  }
  function applySummary(next: CleanupSummary) {
    summary.value = next;
  }
  function applyProgress(event: CleanupProgress) {
    if (event.requestId === requestId.value && stage.value === 'scanning') progress.value = event;
  }

  const latestProgress = (): CleanupProgress | null => progress.value;

  async function scan() {
    if (!(await usePermissionGateStore().require({ feature: 'cleanup' }))) return;
    const id = crypto.randomUUID();
    requestId.value = id;
    pending.value = null;
    progress.value = null;
    error.value = null;
    stage.value = 'scanning';
    try {
      const next = await unwrap(commands.cleanupScan(id, null));
      if (requestId.value !== id) return;
      pending.value = next;
      // Events updated it while awaiting; read through a getter so narrowing from the reset above does not apply.
      const last = latestProgress();
      const total = Math.max(1, last?.total ?? 1);
      const found = next.categories.reduce((sum, category) => sum + category.bytes, 0);
      progress.value = {
        requestId: id,
        category: last?.category ?? 'app_caches',
        location: null,
        checked: total,
        total,
        foundBytes: found
      };
      stage.value = 'finishing';
    } catch (cause) {
      if (requestId.value !== id) return;
      if (cause instanceof IpcError && cause.code === 'E_SCAN_CANCELED') return;
      error.value = failure(cause);
      stage.value = 'failed';
    }
  }

  /** Publish only after the visible progress bar has reached its endpoint. */
  function finishScan(id: string) {
    if (requestId.value !== id || stage.value !== 'finishing' || !pending.value) return;
    report.value = pending.value;
    pending.value = null;
    recommended();
    // Unfold background items that still load, down to their group; everything else starts folded.
    expanded.value = [
      ...new Set(activeServices.value.flatMap(entry => [entry.category.key, groupKey(entry.category.key, entry.group)]))
    ];
    stage.value = 'review';
  }

  async function cancel() {
    const id = requestId.value;
    requestId.value = null;
    pending.value = null;
    stage.value = report.value ? 'review' : 'idle';
    if (id) await unwrap(commands.cleanupCancel(id)).catch(() => undefined);
  }

  /** Always keep the item: protect it and drop it from this report; later scans skip it. */
  async function protect(entry: CleanupEntry) {
    const current = report.value;
    if (!current) return null;
    const rule = await unwrap(commands.cleanupProtect({ reportId: current.reportId, itemId: entry.item.id }));
    rules.value = [...rules.value, rule];
    if (report.value) report.value = withoutItem(report.value, entry.item.id);
    const next = { ...selected.value };
    delete next[entry.item.id];
    selected.value = next;
    return rule;
  }

  /** Undo of protect from the toast: the rule goes away and the item comes back unchanged. */
  async function unprotect(rule: ProtectRule, entry?: CleanupEntry) {
    await unwrap(commands.cleanupUnprotect(rule.id));
    rules.value = rules.value.filter(value => value.id !== rule.id);
    const current = report.value;
    if (!entry || !current) return;
    const putBack = (category: CategoryReport): CategoryReport => ({
      ...category,
      groups: category.groups.some(group => group.id === entry.group.id)
        ? category.groups.map(group =>
            group.id === entry.group.id ? { ...group, items: [...group.items, entry.item] } : group
          )
        : [...category.groups, { ...entry.group, items: [entry.item] }]
    });
    // Protecting the last item removed its category; put it back in report order.
    const keys = current.categories.map(category => category.key);
    const next = keys.includes(entry.category.key)
      ? current.categories.map(category => (category.key === entry.category.key ? putBack(category) : category))
      : [...current.categories, putBack({ ...entry.category, groups: [] })].sort(
          (a, b) => CATEGORIES.indexOf(a.key) - CATEGORIES.indexOf(b.key)
        );
    report.value = { ...current, categories: next };
    setItem(entry.item.id, entry.item.selected);
  }

  /** Settings: protect a folder the user picked; the next scan skips it. */
  async function addRule(path: string) {
    const rule = await unwrap(commands.cleanupProtect({ kind: 'path', value: path }));
    rules.value = [...rules.value, rule];
    return rule;
  }
  async function removeRule(rule: ProtectRule) {
    await unwrap(commands.cleanupUnprotect(rule.id));
    rules.value = rules.value.filter(value => value.id !== rule.id);
  }

  async function confirm() {
    if (!(await usePermissionGateStore().require({ feature: 'cleanup' }))) return;
    const current = report.value;
    if (!current || stage.value !== 'review' || !chosen.value.length) return;
    // Rust rejects stale reports too; rescanning first avoids a round trip and a confusing error.
    if (current.expiresAt <= Date.now()) {
      rescanned.value = true;
      await scan();
      return;
    }
    stage.value = 'submitting';
    try {
      const created = await unwrap(
        commands.cleanupExecute(
          current.reportId,
          chosen.value.map(entry => entry.item.id)
        )
      );
      freeBefore.value = (volume.value ?? current.volume).freeBytes;
      restored.value = false;
      // Task events can arrive before the execute response; keep their newer state.
      if (!tasks.active.some(item => item.id === created.id) && !tasks.finished.some(item => item.id === created.id))
        tasks.applyUpdate(created);
      taskId.value = created.id;
      stage.value = 'running';
      settle();
    } catch (cause) {
      if (cause instanceof IpcError && cause.code === 'E_PLAN_EXPIRED') {
        rescanned.value = true;
        await scan();
        return;
      }
      stage.value = 'review';
      throw cause;
    }
  }

  function settle() {
    const current = task.value;
    if (stage.value !== 'running' || !current) return;
    if (current.state === 'succeeded' || current.state === 'failed' || current.state === 'canceled') {
      stage.value = 'done';
      void loadSummary().catch(() => undefined);
    }
  }
  watch(task, settle, { deep: true });

  // Granting Full Disk Access during review rescans so containers and Safari show up (§06).
  watch(
    () => permissions.fullDiskAccess,
    (next, previous) => {
      if (next === 'granted' && previous !== 'granted' && stage.value === 'review' && report.value?.incomplete.length)
        void scan();
    }
  );

  async function restore() {
    if (!(await usePermissionGateStore().require({ feature: 'cleanup' }))) return null;
    if (!taskId.value) return { restored: 0, missing: [] as string[] };
    const result = await unwrap(commands.uninstallRestore(taskId.value));
    restored.value = result.missing.length === 0;
    void loadSummary().catch(() => undefined);
    return result;
  }

  async function purge() {
    if (!(await usePermissionGateStore().require({ feature: 'cleanup' }))) return null;
    if (!taskId.value) return 0;
    const freed = await unwrap(commands.uninstallPurge(taskId.value));
    restored.value = true;
    void loadSummary().catch(() => undefined);
    return freed;
  }

  /** Back to the overview; the summary keeps the last numbers. */
  function finish() {
    stage.value = 'idle';
    report.value = null;
    selected.value = {};
    expanded.value = [];
    taskId.value = null;
    rescanned.value = false;
    freeBefore.value = null;
  }

  return {
    stage,
    requestId,
    progress,
    report,
    selected,
    expanded,
    summary,
    rules,
    taskId,
    error,
    rescanned,
    freeBefore,
    restored,
    volume,
    scanned,
    categories,
    entries,
    chosen,
    totals,
    inUse,
    activeServices,
    task,
    outcome,
    trashedBytes,
    freedNow,
    isChecked,
    categoryState,
    groupState,
    setItem,
    setCategory,
    setGroup,
    recommended,
    onlyRebuildable,
    toggleExpanded,
    loadSummary,
    loadRules,
    applySummary,
    applyProgress,
    scan,
    finishScan,
    cancel,
    protect,
    unprotect,
    addRule,
    removeRule,
    confirm,
    restore,
    purge,
    finish
  };
});
