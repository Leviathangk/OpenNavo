import type { DataOf } from '@/typings/api/opennavo';
import { request } from '../request';

/** Dashboard overview (07 §7.1). */
export const fetchDashboardOverview = () => request<DataOf<'getDashboardOverview'>>({ url: '/dashboard/overview' });

/** First activity per device/browser today, grouped by Beijing hour. */
export const fetchDashboardUsageIntraday = () =>
  request<DataOf<'getDashboardUsageIntraday'>>({ url: '/dashboard/usage/intraday' });
