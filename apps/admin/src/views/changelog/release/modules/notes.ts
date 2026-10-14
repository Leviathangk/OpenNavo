import type { BodyOf, DataOf, RecordOf, Schemas } from '@/typings/api/opennavo';
import { DEFAULT_SOURCE_LOCALE } from '@/utils/content-locale';
import type { ContentLocale } from '@/utils/content-locale';

/** Match ReleaseSection / EditorialNotesWrite limits (§3.4): at most eight groups and six items per group. */
export const NOTES_LIMITS = { groups: 8, items: 6, area: 12, item: 80, summary: 400, title: 256 } as const;

export interface NotesGroup {
  area: string;
  items: string[];
}

export interface NotesForm {
  sourceLocale: ContentLocale;
  title: string;
  publishedAt: number | null;
  summary: string;
  groups: NotesGroup[];
  bodyMarkdown: string;
}

export const emptyNotes = (sourceLocale: ContentLocale = DEFAULT_SOURCE_LOCALE): NotesForm => ({
  sourceLocale,
  title: '',
  publishedAt: null,
  summary: '',
  groups: [],
  bodyMarkdown: ''
});

/** Populate existing editorial notes from their source language. */
export function notesFromRelease(release: DataOf<'getAdminRelease'>): NotesForm {
  const sourceLocale = release.sourceLocale ?? DEFAULT_SOURCE_LOCALE;
  const source = release.i18n?.find(item => item.locale === sourceLocale);
  return {
    sourceLocale,
    title: source?.title ?? release.title ?? '',
    publishedAt: release.publishedAt ? new Date(release.publishedAt).getTime() : null,
    summary: source?.summary ?? '',
    groups: (source?.sections ?? []).map(section => ({ area: section.area, items: [...section.items] })),
    bodyMarkdown: source?.bodyMarkdown ?? release.bodyMarkdown ?? ''
  };
}

/** Remove blank highlights and empty groups. */
export function normalizeSections(groups: NotesGroup[]): Schemas['ReleaseSection'][] {
  return groups
    .map(group => ({ area: group.area.trim(), items: group.items.map(item => item.trim()).filter(Boolean) }))
    .filter(group => group.area || group.items.length);
}

/** Return the first validation issue's translation key, or null if valid. */
export function validateNotes(form: NotesForm): string | null {
  const summary = form.summary.trim();
  if (!summary) return 'page.changelog.notes.errors.summaryRequired';
  if (summary.length > NOTES_LIMITS.summary) return 'page.changelog.notes.errors.summaryTooLong';
  if (form.title.trim().length > NOTES_LIMITS.title) return 'page.changelog.notes.errors.titleTooLong';
  const sections = normalizeSections(form.groups);
  if (sections.length > NOTES_LIMITS.groups) return 'page.changelog.notes.errors.tooManyGroups';
  for (const section of sections) {
    if (!section.area) return 'page.changelog.notes.errors.areaRequired';
    if (section.area.length > NOTES_LIMITS.area) return 'page.changelog.notes.errors.areaTooLong';
    if (!section.items.length) return 'page.changelog.notes.errors.itemsRequired';
    if (section.items.length > NOTES_LIMITS.items) return 'page.changelog.notes.errors.tooManyItems';
    if (section.items.some(item => item.length > NOTES_LIMITS.item)) return 'page.changelog.notes.errors.itemTooLong';
  }
  return null;
}

type VersionRow = Pick<RecordOf<'listAdminVersions'>, 'version' | 'versionBase' | 'editorialScope'>;

/** A build (0.1,49) owns its notes; any other recorded version writes the note its base version's builds share. */
export function notesVersion(row: Pick<VersionRow, 'version' | 'versionBase'>): string {
  return row.version === row.versionBase ? row.versionBase : row.version;
}

/** The row shows notes of its own, rather than its base version's shared note or nothing. */
export function ownsNotes(row: VersionRow): boolean {
  return row.version === row.versionBase ? row.editorialScope !== 'none' : row.editorialScope === 'build';
}

/** Form → payload: blank title/date/body become null to clear; dates use ISO timestamps. */
export function buildNotesBody(form: NotesForm): BodyOf<'upsertReleaseNotes'> {
  return {
    clear: false,
    sourceLocale: form.sourceLocale,
    summary: form.summary.trim(),
    sections: normalizeSections(form.groups),
    title: form.title.trim() || null,
    bodyMarkdown: form.bodyMarkdown.trim() || null,
    publishedAt: form.publishedAt ? new Date(form.publishedAt).toISOString() : null
  };
}
