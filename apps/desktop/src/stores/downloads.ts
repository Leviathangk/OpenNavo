import { shallowRef } from 'vue';
import { defineStore } from 'pinia';
import type { DownloadDetected, DownloadVerdict } from '@/ipc/bindings';

export interface WatchedDownload {
  token: string;
  path: string;
  /** null while Rust is still checking the file. */
  verdict: DownloadVerdict | null;
}

/**
 * Files a link app's Download brought in (store/security plan §4, §11.2): Rust reports a new file in Downloads
 * (download:detected), then its verdict (download:verified): the signature and Team ID against the registered
 * developer, notarization and the virus scan. Verdicts carry only the path, so the detected event maps it to the app.
 */
export const useDownloadsStore = defineStore('downloads', () => {
  const byToken = shallowRef(new Map<string, WatchedDownload>());
  const tokenOf = new Map<string, string>();
  /** The latest verdict, for the in-app notice. */
  const last = shallowRef<WatchedDownload | null>(null);
  /** The latest threat the Downloads watch found in a file no link app announced (plan §6, S1). */
  const threat = shallowRef<{ path: string; signature: string } | null>(null);

  function put(entry: WatchedDownload) {
    byToken.value = new Map(byToken.value).set(entry.token, entry);
  }

  function detected(event: DownloadDetected) {
    tokenOf.set(event.path, event.token);
    put({ token: event.token, path: event.path, verdict: null });
  }

  function verified(verdict: DownloadVerdict) {
    const token = tokenOf.get(verdict.path);
    // A verdict for a file no link app's Download announced has no app page; only a threat is worth a notice.
    if (!token) {
      const signature = verdict.av?.signature;
      if (signature) threat.value = { path: verdict.path, signature };
      return;
    }
    const entry = { token, path: verdict.path, verdict };
    put(entry);
    last.value = entry;
  }

  const forToken = (token: string) => byToken.value.get(token) ?? null;

  return { last, threat, detected, verified, forToken };
});
