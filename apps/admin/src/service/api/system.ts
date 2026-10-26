import type { BodyOf, DataOf, QueryOf } from '@/typings/api/opennavo';
import { request } from '../request';

// Administrators, roles/permissions, audit logs, profile (07 §7.14, §7.15).

export const fetchAdminUsers = (params: QueryOf<'listAdminUsers'>) =>
  request<DataOf<'listAdminUsers'>>({ url: '/system/users', params });

export const createAdminUser = (data: BodyOf<'createAdminUser'>) =>
  request<DataOf<'createAdminUser'>>({ url: '/system/users', method: 'post', data });

export const updateAdminUser = (id: number, data: BodyOf<'updateAdminUser'>) =>
  request({ url: `/system/users/${id}`, method: 'put', data });

export const deleteAdminUser = (id: number) => request({ url: `/system/users/${id}`, method: 'delete' });

export const resetAdminUserPassword = (id: number, data: BodyOf<'resetAdminUserPassword'>) =>
  request({ url: `/system/users/${id}/password`, method: 'put', data });

export const forceLogoutAdminUser = (id: number) =>
  request({ url: `/system/users/${id}/force-logout`, method: 'post' });

export const fetchRoles = () => request<DataOf<'listRoles'>>({ url: '/system/roles' });

export const createRole = (data: BodyOf<'createRole'>) =>
  request<DataOf<'createRole'>>({ url: '/system/roles', method: 'post', data });

export const updateRole = (id: number, data: BodyOf<'updateRole'>) =>
  request({ url: `/system/roles/${id}`, method: 'put', data });

export const deleteRole = (id: number) => request({ url: `/system/roles/${id}`, method: 'delete' });

export const setRolePermissions = (id: number, data: BodyOf<'setRolePermissions'>) =>
  request({ url: `/system/roles/${id}/permissions`, method: 'put', data });

export const fetchPermissions = () => request<DataOf<'listPermissions'>>({ url: '/system/permissions' });

export const fetchAuditLogs = (params: QueryOf<'listAuditLogs'>) =>
  request<DataOf<'listAuditLogs'>>({ url: '/system/audit-logs', params });

export const fetchProfile = () => request<DataOf<'getProfile'>>({ url: '/profile' });

export const updateProfile = (data: BodyOf<'updateProfile'>) => request({ url: '/profile', method: 'put', data });

export const changePassword = (data: BodyOf<'changePassword'>) =>
  request({ url: '/profile/password', method: 'put', data });

export const fetchGitHubSettings = () => request<DataOf<'getGitHubSettings'>>({ url: '/system/github-settings' });

export const updateGitHubSettings = (data: BodyOf<'updateGitHubSettings'>) =>
  request<DataOf<'updateGitHubSettings'>>({ url: '/system/github-settings', method: 'put', data });

export const fetchSeoSettings = () => request<DataOf<'getSeoSettings'>>({ url: '/system/seo-settings' });

export const updateSeoSettings = (data: BodyOf<'updateSeoSettings'>) =>
  request<DataOf<'updateSeoSettings'>>({ url: '/system/seo-settings', method: 'put', data });

export const fetchIndexReport = (params: QueryOf<'listIndexReport'>) =>
  request<DataOf<'listIndexReport'>>({ url: '/seo/index-report', params });
