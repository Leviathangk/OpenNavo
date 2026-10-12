import type { OnChipTone } from '@opennavo/ui';
import type { CleanupCategory, CleanupRisk } from '@/ipc/bindings';

/** Category icons (cleanup proposal §05); names must be bundled in @opennavo/ui. */
export const CATEGORY_ICONS: Record<CleanupCategory, string> = {
  app_caches: 'layers',
  logs: 'file-text',
  browser_caches: 'globe',
  dev_caches: 'code-xml',
  homebrew: 'package',
  temp: 'clock',
  leftovers: 'scan-search',
  installers: 'download',
  project_artifacts: 'folder',
  trash: 'trash',
  device_backups: 'hard-drive',
  docker: 'container',
  large_files: 'boxes'
};

export const RISK_TONES: Record<CleanupRisk, OnChipTone> = {
  rebuildable: 'success',
  review: 'warning',
  personal: 'danger'
};
