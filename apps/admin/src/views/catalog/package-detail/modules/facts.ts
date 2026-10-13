import type { BodyOf, DataOf, Schemas } from '@/typings/api/opennavo';

// Form models and request bodies for the facts tab (WP12). The server re-validates everything;
// these checks only explain problems before a round trip.

export type Facts = DataOf<'getPackageFacts'>;
type Evidence = {
  sourceUrl: string;
  method: Schemas['FactMethod'];
  verifiedAt: string;
  appVersion?: string | null;
  status: Schemas['FactStatus'];
  note?: string | null;
};

export interface EvidenceForm {
  sourceUrl: string;
  method: Schemas['FactMethod'] | null;
  /** Timestamp; null sends nothing so the server records now. */
  verifiedAt: number | null;
  appVersion: string;
  status: Schemas['FactStatus'];
  note: string;
}

export function evidenceForm(value?: Evidence | null): EvidenceForm {
  return {
    sourceUrl: value?.sourceUrl ?? '',
    method: value?.method ?? null,
    verifiedAt: value ? Date.parse(value.verifiedAt) : null,
    appVersion: value?.appVersion ?? '',
    status: value?.status ?? 'verified',
    note: value?.note ?? ''
  };
}

export const evidenceValid = (form: EvidenceForm) =>
  /^https:\/\/[^\s/?#]+\S*$/.test(form.sourceUrl.trim()) && form.method !== null;

function evidenceBody(form: EvidenceForm) {
  return {
    sourceUrl: form.sourceUrl.trim(),
    method: form.method,
    verifiedAt: form.verifiedAt === null ? null : new Date(form.verifiedAt).toISOString(),
    appVersion: form.appVersion.trim() || null,
    status: form.status,
    note: form.note.trim() || null
  };
}

export type PriceType = Schemas['PriceType'];
type Period = NonNullable<BodyOf<'setPackagePrice'>['period']>;
/** Types that may carry an amount: the price itself, the paid upgrade, or the price after the trial. */
export const AMOUNT_TYPES: PriceType[] = ['paid', 'subscription', 'trial', 'free_with_iap'];

export interface PriceForm {
  type: PriceType;
  amount: number | null;
  currency: string;
  period: Period | null;
  region: string;
  plan: string;
  evidence: EvidenceForm;
}

export function priceForm(price: Facts['price']): PriceForm {
  return {
    type: price?.type ?? 'unknown',
    amount: price?.amount ?? null,
    currency: price?.currency ?? '',
    period: price?.period ?? null,
    region: price?.region ?? '',
    plan: price?.plan ?? '',
    evidence: evidenceForm(price)
  };
}

/** Returns the problem's message key, or null when the form can be sent. */
export function priceProblem(form: PriceForm): 'sourceRequired' | 'amountInvalid' | null {
  if (form.type === 'unknown') return null;
  if (!evidenceValid(form.evidence)) return 'sourceRequired';
  const amount = AMOUNT_TYPES.includes(form.type) ? form.amount : null;
  if (amount === null) return null;
  const cents = Math.round(amount * 100);
  if (amount <= 0 || amount > 1e7 || Math.abs(cents - amount * 100) > 1e-6) return 'amountInvalid';
  if (!/^[A-Z]{3}$/.test(form.currency.trim()) || !form.period) return 'amountInvalid';
  if (form.region.trim() && !/^[A-Z]{2}$/.test(form.region.trim())) return 'amountInvalid';
  if (
    (form.type === 'paid' && form.period !== 'one_time') ||
    (form.type === 'subscription' && form.period === 'one_time')
  )
    return 'amountInvalid';
  return null;
}

export function priceBody(form: PriceForm): BodyOf<'setPackagePrice'> {
  if (form.type === 'unknown') return { type: 'unknown' };
  const amount = AMOUNT_TYPES.includes(form.type) ? form.amount : null;
  return {
    type: form.type,
    amount,
    currency: amount === null ? null : form.currency.trim(),
    period: amount === null ? null : form.period,
    region: amount === null ? null : form.region.trim() || null,
    plan: form.plan.trim() || null,
    ...evidenceBody(form.evidence)
  };
}

export type Arch = 'arm64' | 'x86_64';
export const ARCHS: Arch[] = ['arm64', 'x86_64'];
type Support = Schemas['MacosRange']['support'];
export interface RangeForm {
  include: boolean;
  support: Support;
  minimum: string;
  maximum: string;
}
export interface MacosForm {
  ranges: Record<Arch, RangeForm>;
  evidence: EvidenceForm;
}

export function macosForm(macos: Facts['macos']): MacosForm {
  const range = (arch: Arch): RangeForm => {
    const found = macos?.ranges.find(item => item.arch === arch);
    return {
      include: Boolean(found),
      support: found?.support ?? 'native',
      minimum: found?.minimum ?? '',
      maximum: found?.maximum ?? ''
    };
  };
  return { ranges: { arm64: range('arm64'), x86_64: range('x86_64') }, evidence: evidenceForm(macos) };
}

const MACOS_VERSION = /^(?:10\.(?:[4-9]|1[0-9])|1[1-9]|[2-9][0-9])(?:\.[0-9]{1,2}){0,2}$/;

function compareVersions(left: string, right: string) {
  const a = left.split('.').map(Number);
  const b = right.split('.').map(Number);
  for (let index = 0; index < Math.max(a.length, b.length); index += 1) {
    const difference = (a[index] ?? 0) - (b[index] ?? 0);
    if (difference) return difference;
  }
  return 0;
}

export function macosProblem(form: MacosForm): 'sourceRequired' | 'rangesRequired' | null {
  const included = ARCHS.filter(arch => form.ranges[arch].include);
  if (!included.length) return 'rangesRequired';
  for (const arch of included) {
    const { support, minimum, maximum } = form.ranges[arch];
    if (support === 'rosetta' && arch !== 'arm64') return 'rangesRequired';
    if (support === 'unsupported') continue;
    const low = minimum.trim();
    const high = maximum.trim();
    if ((low && !MACOS_VERSION.test(low)) || (high && !MACOS_VERSION.test(high))) return 'rangesRequired';
    if (low && high && compareVersions(low, high) > 0) return 'rangesRequired';
  }
  return evidenceValid(form.evidence) ? null : 'sourceRequired';
}

export function macosBody(form: MacosForm): BodyOf<'setPackageMacosSupport'> {
  const ranges = ARCHS.filter(arch => form.ranges[arch].include).map(arch => {
    const { support, minimum, maximum } = form.ranges[arch];
    const versions = support !== 'unsupported';
    return {
      arch,
      support,
      minimum: versions ? minimum.trim() || null : null,
      maximum: versions ? maximum.trim() || null : null
    };
  });
  return { ranges, ...evidenceBody(form.evidence) };
}

export type AlternativeRow = Facts['alternatives'][number];
export const MAX_ALTERNATIVES = 8;
export const REASONS: Schemas['AlternativeReason'][] = [
  'free',
  'open_source',
  'lighter',
  'more_features',
  'simpler',
  'native_ui',
  'apple_silicon',
  'privacy',
  'offline',
  'collaboration',
  'cross_platform',
  'one_time_purchase',
  'active_development',
  'customizable'
];
export const LIMITS: Schemas['AlternativeLimit'][] = [
  'paid',
  'subscription',
  'fewer_features',
  'requires_account',
  'learning_curve',
  'different_workflow',
  'no_apple_silicon',
  'online_only',
  'limited_languages'
];
export const AUDIENCE = ['everyday', 'professionals', 'developers', 'teams', 'beginners', 'power_users'] as const;

export const alternativesValid = (items: AlternativeRow[]) =>
  items.length <= MAX_ALTERNATIVES &&
  items.every(item => item.reasons.length >= 1 && item.reasons.length <= 4 && item.limits.length <= 3);

export function alternativesBody(items: AlternativeRow[]): BodyOf<'setPackageAlternatives'> {
  return {
    items: items.map(item => ({
      packageId: item.packageId,
      reasons: item.reasons,
      limits: item.limits,
      bestFor: item.bestFor ?? null,
      note: item.note?.trim() || null
    }))
  };
}

/** price.unknown → priceUnknown, the message key of a gap code. */
export const gapKey = (code: string) => code.replace(/\.(\w)/g, (_, letter: string) => letter.toUpperCase());
