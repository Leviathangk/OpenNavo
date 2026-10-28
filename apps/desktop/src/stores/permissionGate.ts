import { computed, ref } from 'vue';
import { defineStore } from 'pinia';
import type { TaskTarget, TaskTrigger } from '@/ipc/bindings';
import { usePermissionsStore, gateReady } from './permissions';

export type GateFeature = 'security' | 'firewall' | 'monitor' | 'installed' | 'updates' | 'cleanup' | 'install';
export interface GateRequest {
  feature: GateFeature;
  targets?: readonly TaskTarget[];
  resume?: { op: 'install'; trigger: TaskTrigger };
}
const storageKey = 'opennavo.permissionGate.resume';
export const usePermissionGateStore = defineStore('permissionGate', () => {
  const permissions = usePermissionsStore();
  const ready = computed(
    () =>
      Boolean(document.documentElement.dataset.opennavoWindow) ||
      gateReady(permissions.status, permissions.unknownConfirmed)
  );
  const current = ref<GateRequest | null>(null);
  const shown = new Set<GateFeature>();
  let promise: Promise<boolean> | null = null;
  let resolvePending: ((value: boolean) => void) | null = null;
  let waiting: GateRequest[] | null = null;
  let resumable: GateRequest | null = null;
  // Includes installs still preparing or waiting for their turn, before they reach require().
  const pendingInstalls = new Set<{ target: TaskTarget; trigger: TaskTrigger }>();
  let rememberedRoute: string | null = null;

  function trackInstall(target: TaskTarget, trigger: TaskTrigger) {
    if (trigger === 'schedule' || trigger === 'retry' || document.documentElement.dataset.opennavoWindow)
      return () => {};
    const entry = { target, trigger };
    pendingInstalls.add(entry);
    writeRemembered();
    return () => {
      if (!pendingInstalls.delete(entry)) return;
      if (resumable) {
        const targets = resumable.targets?.filter(item => item.kind !== target.kind || item.token !== target.token);
        resumable = targets?.length ? { ...resumable, targets } : null;
      }
      if (rememberedRoute !== null && !pendingInstalls.size && !resumable) clearRemembered();
      else writeRemembered();
    };
  }

  function open(request: GateRequest) {
    if (request.resume && request.targets?.length) {
      const targets = [...(resumable?.targets ?? []), ...request.targets];
      resumable = {
        feature: 'install',
        resume: request.resume,
        targets: targets.filter(
          (target, index) =>
            targets.findIndex(item => item.kind === target.kind && item.token === target.token) === index
        )
      };
    }
    if (current.value) {
      const targets = [...(current.value.targets ?? []), ...(request.targets ?? [])];
      current.value = {
        ...current.value,
        ...request,
        targets: targets.filter(
          (target, index) =>
            targets.findIndex(item => item.kind === target.kind && item.token === target.token) === index
        )
      };
    } else {
      current.value = request;
      void permissions.refresh().catch(() => undefined);
    }
    shown.add(request.feature);
    promise ??= new Promise<boolean>(resolve => {
      resolvePending = resolve;
    });
    writeRemembered();
    return promise;
  }
  function show(feature: GateFeature, force = false) {
    if (ready.value || !permissions.status || current.value || (!force && shown.has(feature))) return;
    void open({ feature });
  }
  function require(request: GateRequest): Promise<boolean> {
    if (current.value) return open(request);
    if (ready.value) return Promise.resolve(true);
    if (!permissions.status) {
      if (waiting) {
        waiting.push(request);
        return promise!;
      }
      waiting = [request];
      promise = new Promise<boolean>(resolve => {
        resolvePending = resolve;
      });
      void permissions
        .refresh()
        .catch(() => undefined)
        .then(() => {
          const requests = waiting;
          waiting = null;
          if (!requests) return;
          if (!permissions.status) finish(false);
          else if (ready.value && !current.value) finish(true);
          else for (const queuedRequest of requests) void open(queuedRequest);
        });
      return promise;
    }
    return open(request);
  }
  function offer(request: GateRequest): Promise<boolean> {
    return open(request);
  }
  function finish(value: boolean) {
    const resolve = resolvePending;
    resolvePending = null;
    promise = null;
    current.value = null;
    resumable = null;
    waiting = null;
    clearRemembered();
    resolve?.(value);
  }
  function clearRemembered() {
    rememberedRoute = null;
    try {
      localStorage.removeItem(storageKey);
    } catch {
      /* Storage may be unavailable. */
    }
  }
  function proceed() {
    if (ready.value) finish(true);
  }
  function dismiss() {
    finish(false);
  }
  function rememberForRelaunch(route: string) {
    if (!current.value) return;
    rememberedRoute = route;
    writeRemembered();
  }
  function writeRemembered() {
    if (rememberedRoute === null || !current.value) return;
    const installs = [...pendingInstalls];
    const targets = [...installs.map(entry => entry.target), ...(resumable?.targets ?? [])];
    const resume = installs.length ? { op: 'install' as const, trigger: installs[0]!.trigger } : resumable?.resume;
    const request: GateRequest =
      targets.length && resume
        ? {
            feature: 'install',
            resume,
            targets: targets.filter(
              (target, index) =>
                targets.findIndex(item => item.kind === target.kind && item.token === target.token) === index
            )
          }
        : { ...current.value, resume: undefined };
    try {
      localStorage.setItem(storageKey, JSON.stringify({ route: rememberedRoute, request, at: Date.now() }));
    } catch {
      /* Storage may be unavailable. */
    }
  }
  function takeRemembered(): { route: string; request: GateRequest } | null {
    rememberedRoute = null;
    try {
      const raw = localStorage.getItem(storageKey);
      localStorage.removeItem(storageKey);
      if (!raw) return null;
      const value = JSON.parse(raw);
      const age = Date.now() - value.at;
      if (
        typeof value.route !== 'string' ||
        !value.route.startsWith('/') ||
        !value.request ||
        !['security', 'firewall', 'monitor', 'installed', 'updates', 'cleanup', 'install'].includes(
          value.request.feature
        ) ||
        !Number.isFinite(age) ||
        age < 0 ||
        age > 600000
      )
        return null;
      const request = value.request;
      if (
        request.targets !== undefined &&
        (!Array.isArray(request.targets) ||
          !request.targets.every(
            (target: TaskTarget) =>
              target &&
              ['cask', 'formula', 'mas', 'link', 'app'].includes(target.kind) &&
              typeof target.token === 'string'
          ))
      )
        return null;
      if (
        request.resume !== undefined &&
        (request.resume?.op !== 'install' ||
          !['manual', 'schedule', 'deeplink', 'bundle', 'retry'].includes(request.resume.trigger))
      )
        return null;
      return { route: value.route, request };
    } catch {
      return null;
    }
  }
  return { ready, current, show, require, offer, proceed, dismiss, trackInstall, rememberForRelaunch, takeRemembered };
});
