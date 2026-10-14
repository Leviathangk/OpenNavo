import type { Schemas } from '@/typings/api/opennavo';
import { $t } from '@/locales';

/** All historical job types, including retired types for display. */
export const JOB_TYPES: Schemas['JobType'][] = [
  'catalog_sync',
  'analytics_sync',
  'snapshot_build',
  'changelog_schedule',
  'assets_download-size',
  'cleanup',
  'facts_sources',
  'taps_sync',
  'enrich_schedule',
  'translate_schedule',
  'assets_icons'
];

/** Manually triggerable jobs, matching TriggerJobType. */
export const TRIGGER_TYPES: Schemas['TriggerJobType'][] = [
  'catalog_sync',
  'analytics_sync',
  'snapshot_build',
  'changelog_schedule',
  'assets_download-size',
  'cleanup',
  'facts_sources',
  'taps_sync'
];

/** History uses asynq names (assets:download-size); replace colons with underscores for translation lookup, preserving unknown names. */
export function jobLabel(type: string): string {
  const key = type.replace(/:/g, '_') as Schemas['JobType'];
  return JOB_TYPES.includes(key) ? $t(`page.ops.job.types.${key}`) : type;
}
