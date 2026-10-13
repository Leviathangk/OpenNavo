import type { BodyOf, DataOf, QueryOf } from '@/typings/api/opennavo';
import { request } from '../request';

// Revisions, recycle bin, request-wide undo, AI translation logs (M8-10, 07 §10.4).

export const fetchContentRevisions = (params: QueryOf<'listContentRevisions'>) =>
  request<DataOf<'listContentRevisions'>>({ url: '/content/revisions', params });

export const fetchContentRevision = (id: number) =>
  request<DataOf<'getContentRevision'>>({ url: `/content/revisions/${id}` });

export const restoreRevision = (id: number, data: BodyOf<'restoreRevision'>) =>
  request<DataOf<'restoreRevision'>>({ url: `/content/revisions/${id}/restore`, method: 'post', data });

export const fetchTrash = (params: QueryOf<'listTrash'>) =>
  request<DataOf<'listTrash'>>({ url: '/content/trash', params });

export const fetchTrashItem = (id: number) => request<DataOf<'getTrashItem'>>({ url: `/content/trash/${id}` });

export const restoreTrash = (id: number) =>
  request<DataOf<'restoreTrash'>>({ url: `/content/trash/${id}/restore`, method: 'post' });

export const revertRequest = (requestId: string) =>
  request<DataOf<'revertRequest'>>({
    url: `/content/requests/${encodeURIComponent(requestId)}/revert`,
    method: 'post'
  });

export const fetchTranslationLogs = (params: QueryOf<'listTranslationLogs'>) =>
  request<DataOf<'listTranslationLogs'>>({ url: '/translations/logs', params });

export const fetchTranslationLog = (id: number) =>
  request<DataOf<'getTranslationLog'>>({ url: `/translations/logs/${id}` });
