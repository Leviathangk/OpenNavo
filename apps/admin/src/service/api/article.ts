import type { BodyOf, DataOf, QueryOf, Schemas } from '@/typings/api/opennavo';
import { request } from '../request';

// Articles (WP14/WP16): every write returns the article after the change; expectedRevision is the
// article revision from the last read.

type Article = DataOf<'getAdminArticle'>;

export const fetchArticleList = (params: QueryOf<'listAdminArticles'>) =>
  request<DataOf<'listAdminArticles'>>({ url: '/articles', params });

export const fetchArticle = (id: number) => request<Article>({ url: `/articles/${id}` });

export const createArticle = (data: BodyOf<'createArticle'>) =>
  request<Article>({ url: '/articles', method: 'post', data });

export const updateArticle = (id: number, data: BodyOf<'updateArticle'>) =>
  request<Article>({ url: `/articles/${id}`, method: 'put', data });

export const setArticlePackages = (id: number, data: BodyOf<'setArticlePackages'>) =>
  request<Article>({ url: `/articles/${id}/packages`, method: 'put', data });

export const submitArticle = (id: number, expectedRevision: number) =>
  request<Article>({ url: `/articles/${id}/submit`, method: 'post', data: { expectedRevision } });

export const publishArticle = (id: number, expectedRevision: number) =>
  request<Article>({ url: `/articles/${id}/publish`, method: 'post', data: { expectedRevision } });

export const withdrawArticle = (id: number, expectedRevision: number, reason: string | null) =>
  request<Article>({ url: `/articles/${id}/withdraw`, method: 'post', data: { expectedRevision, reason } });

export const restoreArticle = (id: number, expectedRevision: number) =>
  request<Article>({ url: `/articles/${id}/restore`, method: 'post', data: { expectedRevision } });

export const revertArticle = (id: number, expectedRevision: number, revisionNumber: number) =>
  request<Article>({ url: `/articles/${id}/revert`, method: 'post', data: { expectedRevision, revisionNumber } });

export const setArticleSlug = (id: number, expectedRevision: number, slug: string) =>
  request<Article>({ url: `/articles/${id}/slug`, method: 'put', data: { expectedRevision, slug } });

export const setArticleLocale = (id: number, locale: Schemas['Locale'], expectedRevision: number, withheld: boolean) =>
  request<Article>({ url: `/articles/${id}/locales/${locale}`, method: 'put', data: { expectedRevision, withheld } });

export const fetchArticleAuthors = () => request<DataOf<'listArticleAuthors'>>({ url: '/article-authors' });

export const createArticleAuthor = (data: BodyOf<'createArticleAuthor'>) =>
  request<DataOf<'createArticleAuthor'>>({ url: '/article-authors', method: 'post', data });

export const updateArticleAuthor = (id: number, data: BodyOf<'updateArticleAuthor'>) =>
  request<DataOf<'updateArticleAuthor'>>({ url: `/article-authors/${id}`, method: 'put', data });

export const captureArticleEvidence = (id: number, data: BodyOf<'captureArticleEvidence'>) =>
  request<DataOf<'captureArticleEvidence'>>({ url: `/articles/${id}/evidence`, method: 'post', data });

export const createArticleTestArtifact = (id: number, data: BodyOf<'createArticleTestArtifact'>) =>
  request<DataOf<'createArticleTestArtifact'>>({ url: `/articles/${id}/test-artifacts`, method: 'post', data });
