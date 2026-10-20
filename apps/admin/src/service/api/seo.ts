import type { DataOf, QueryOf } from '@/typings/api/opennavo';
import { request } from '../request';

export const fetchSeoOverview = () => request<DataOf<'getSeoOverview'>>({ url: '/seo/overview' });

export const fetchSeoAlerts = (params: QueryOf<'listSeoAlerts'>) =>
  request<DataOf<'listSeoAlerts'>>({ url: '/seo/alerts', params });

export const fetchSeoGeo = (id: number) => request<DataOf<'getSeoGeo'>>({ url: `/seo/geo/${id}` });
