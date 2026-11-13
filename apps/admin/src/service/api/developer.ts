import { request } from '../request';

export interface DeveloperSubmission {
  id: number;
  publicToken: string;
  kind: 'new' | 'claim' | 'location';
  status: 'submitted' | 'in_review' | 'changes_requested' | 'approved' | 'rejected' | 'published';
  sourceKind: string;
  sourceUrl: string;
  packageId: number | null;
  payload: Record<string, unknown>;
  contactEmail: string;
  proof: string;
  reviewerId: number | null;
  decisionNote: string | null;
  createdAt: string;
  updatedAt: string;
  decidedAt: string | null;
}

export interface DeveloperSubmissionPatch {
  status?: DeveloperSubmission['status'];
  decisionNote?: string | null;
  payload?: Record<string, unknown>;
  packageKind?: string;
  packageToken?: string;
  feature?: boolean;
  quote?: string;
  sort?: number;
}

export const fetchDeveloperSubmissions = (params?: { status?: string; q?: string; limit?: number }) =>
  request<DeveloperSubmission[]>({ url: '/developer/submissions', params });

export const fetchDeveloperSubmission = (id: number) =>
  request<DeveloperSubmission>({ url: `/developer/submissions/${id}` });

export const updateDeveloperSubmission = (id: number, data: DeveloperSubmissionPatch) =>
  request<DeveloperSubmission>({ url: `/developer/submissions/${id}`, method: 'patch', data });

export interface DeveloperPickRow {
  packageId: number;
  kind: string;
  token: string;
  name: string;
  iconUrl: string | null;
  quote: string;
  sort: number;
  active: boolean;
  publishedAt: string;
  submissionId: number | null;
}

export const fetchDeveloperPicks = () => request<DeveloperPickRow[]>({ url: '/developer/picks' });

export const setDeveloperPick = (data: { packageKind?: string; packageToken: string; quote?: string; sort?: number; active?: boolean }) =>
  request<{ packageId: number; active: boolean }>({ url: '/developer/picks', method: 'post', data });
