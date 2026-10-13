import type { BodyOf, DataOf, Schemas } from '@/typings/api/opennavo';

// Form model for the article editor (WP16). The server validates again; these helpers only decide
// what changed and catch obvious mistakes before a round trip.

export type Article = DataOf<'getAdminArticle'>;
export type ArticleType = Schemas['ArticleType'];
export const ARTICLE_TYPES: ArticleType[] = ['recommendation', 'review', 'tutorial', 'roundup', 'news'];
export const ARTICLE_SLUG = /^[a-z0-9]+(-[a-z0-9]+)*$/;
export const MAX_SOURCES = 20;
export const MAX_PACKAGES = 50;

export interface ArticlePackage {
  id: number;
  token: string;
  displayName: string;
  iconUrl: string | null;
}

export interface ArticleForm {
  sourceLocale: Schemas['Locale'];
  type: ArticleType;
  title: string;
  summary: string;
  bodyMarkdown: string;
  testNote: string;
  seoTitle: string;
  seoDescription: string;
  authorId: number | null;
  coverAssetId: number | null;
  coverUrl: string | null;
  coverAlt: string;
  sources: Schemas['ArticleSource'][];
  packages: ArticlePackage[];
}

export function formFromView(view: Article): ArticleForm {
  const working = view.working;
  return {
    sourceLocale: working.sourceLocale,
    type: working.type,
    title: working.title,
    summary: working.summary ?? '',
    bodyMarkdown: working.bodyMarkdown ?? '',
    testNote: working.testNote ?? '',
    seoTitle: working.seoTitle ?? '',
    seoDescription: working.seoDescription ?? '',
    authorId: working.authorId ?? null,
    coverAssetId: working.coverAssetId ?? null,
    coverUrl: working.coverUrl ?? null,
    coverAlt: working.coverAlt ?? '',
    sources: working.sources.map(source => ({ ...source })),
    packages: working.packages.map(item => ({
      id: item.id,
      token: item.token,
      displayName: item.displayName,
      iconUrl: item.iconUrl ?? null
    }))
  };
}

type Patch = Omit<BodyOf<'updateArticle'>, 'expectedRevision'>;

const optional = (value: string) => value.trim() || null;

/** Fields that differ from the loaded working revision; empty text clears the field. */
export function changedFields(form: ArticleForm, view: Article): Patch {
  const base = formFromView(view);
  const patch: Patch = {};
  if (form.sourceLocale !== base.sourceLocale) patch.sourceLocale = form.sourceLocale;
  if (form.type !== base.type) patch.type = form.type;
  if (form.title.trim() !== base.title) patch.title = form.title.trim();
  for (const key of ['summary', 'bodyMarkdown', 'testNote', 'seoTitle', 'seoDescription', 'coverAlt'] as const) {
    if (form[key].trim() !== base[key]) patch[key] = optional(form[key]);
  }
  if (form.authorId !== base.authorId) patch.authorId = form.authorId;
  if (form.coverAssetId !== base.coverAssetId) {
    patch.coverAssetId = form.coverAssetId;
    patch.coverAlt = form.coverAssetId ? optional(form.coverAlt) : null;
  }
  const sources = form.sources.map(source => ({ title: source.title.trim(), url: source.url.trim() }));
  if (JSON.stringify(sources) !== JSON.stringify(base.sources)) patch.sources = sources;
  return patch;
}

export const packagesChanged = (form: ArticleForm, view: Article) =>
  form.packages.map(item => item.id).join(',') !== view.working.packages.map(item => item.id).join(',');

/** Problems the form can see before saving: an empty title, incomplete sources or a cover without alt text. */
export function formProblems(form: ArticleForm): string[] {
  const out: string[] = [];
  if (!form.title.trim()) out.push('title');
  if (form.sources.some(source => !source.title.trim() || !/^https:\/\/\S+$/.test(source.url.trim())))
    out.push('sources');
  if (form.coverAssetId && !form.coverAlt.trim()) out.push('cover_alt');
  return out;
}
