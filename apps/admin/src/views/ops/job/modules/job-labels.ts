import type { Schemas } from '@/typings/api/opennavo';
import { $t } from '@/locales';

/** Job types with localized labels, including retired types for display. */
export const JOB_TYPES = [
  'catalog_sync',
  'analytics_sync',
  'snapshot_build',
  'github-release_snapshot',
  'changelog_schedule',
  'assets_download-size',
  'cleanup',
  'facts_sources',
  'taps_sync',
  'enrich_schedule',
  'translate_schedule',
  'assets_icons'
] as const satisfies Schemas['JobType'][];

/** Manually triggerable jobs, matching TriggerJobType. */
export const TRIGGER_TYPES: Schemas['TriggerJobType'][] = [
  'catalog_sync',
  'analytics_sync',
  'snapshot_build',
  'github-release_snapshot',
  'changelog_schedule',
  'assets_download-size',
  'cleanup',
  'facts_sources',
  'taps_sync'
];

/** Accept public job names and legacy asynq names, preserving unknown labels. */
export function jobLabel(type: string): string {
  const key = type.replace(/:/g, '_');
  const known = JOB_TYPES.find(value => value === key);
  return known ? $t(`page.ops.job.types.${known}`) : type;
}
