export type HermesProvider =
  | 'nous'
  | 'codex'
  | 'claude-code'
  | 'copilot'
  | 'opencode'
  | 'opencode-go'
  | 'openai'
  | 'anthropic'
  | 'google'
  | 'openrouter'
  | 'local'
  | 'custom';
export type HermesEffort = 'none' | 'minimal' | 'low' | 'medium' | 'high' | 'xhigh' | 'max' | 'ultra';
export type HermesLoginProvider = 'nous' | 'codex' | 'claude-code' | 'copilot';

export interface HermesProviderInfo {
  id: HermesProvider;
  models: readonly string[];
  /** OAuth providers already have an account; the rest take a key or endpoint. */
  oauth: boolean;
  /** Subscription/CLI login provider, when one is available. */
  login?: HermesLoginProvider;
  /** Local OpenCode auth store key, used when the API-key field is empty. */
  localAuth?: 'opencode' | 'opencode-go';
  endpoint?: string;
}

export const HERMES_PROVIDERS: readonly HermesProviderInfo[] = [
  { id: 'nous', models: ['Hermes 4 · 405B', 'Hermes 4 · 70B'], oauth: true, login: 'nous' },
  { id: 'codex', models: ['GPT-5.3 Codex', 'GPT-5.2 Codex'], oauth: false, login: 'codex' },
  { id: 'claude-code', models: ['Claude Sonnet 4.5', 'Claude Opus 4.5'], oauth: false, login: 'claude-code' },
  { id: 'copilot', models: ['GPT-4o', 'Claude Sonnet 4.5'], oauth: false, login: 'copilot' },
  { id: 'opencode', models: ['Kimi K2.6', 'GPT-5.5', 'Claude Sonnet 4.5'], oauth: false, localAuth: 'opencode' },
  { id: 'opencode-go', models: ['Kimi K3', 'GLM-5.2', 'DeepSeek V4 Pro'], oauth: false, localAuth: 'opencode-go' },
  { id: 'openai', models: ['GPT-5.6', 'GPT-5.6 mini'], oauth: false },
  { id: 'anthropic', models: ['Claude Sonnet 4.5', 'Claude Opus 4.5'], oauth: false },
  { id: 'google', models: ['Gemini 3 Pro', 'Gemini 3 Flash'], oauth: false },
  { id: 'openrouter', models: ['Hermes 4 · 405B', 'Claude Sonnet 4.5', 'GPT-5.6'], oauth: false },
  { id: 'local', models: ['Llama 4 · 405B', 'Qwen 3 · 235B'], oauth: false, endpoint: 'http://127.0.0.1:11434' },
  { id: 'custom', models: ['Custom model'], oauth: false }
];

export const HERMES_PROVIDER_MAP = Object.fromEntries(
  HERMES_PROVIDERS.map(provider => [provider.id, provider])
) as Record<HermesProvider, HermesProviderInfo>;

export const HERMES_EFFORTS: readonly HermesEffort[] = [
  'none',
  'minimal',
  'low',
  'medium',
  'high',
  'xhigh',
  'max',
  'ultra'
];

export function shortModel(value: string): string {
  return value.split(' · ')[0] ?? value;
}

export function providerModels(provider: HermesProvider): readonly string[] {
  return HERMES_PROVIDER_MAP[provider].models;
}

export function defaultProviderEndpoint(provider: HermesProvider): string {
  return HERMES_PROVIDER_MAP[provider].endpoint ?? '';
}

/**
 * Display labels are product copy; providers receive canonical model ids.
 * Keep this table in sync with the models the pinned Hermes runtime accepts.
 */
const MODEL_IDS: Record<string, string> = {
  'Hermes 4 · 405B': 'nousresearch/hermes-4-405b',
  'Hermes 4 · 70B': 'nousresearch/hermes-4-70b',
  'Kimi K2.6': 'kimi-k2.6',
  'Kimi K3': 'kimi-k3',
  'GPT-5.5': 'gpt-5.5',
  'GLM-5.2': 'glm-5.2',
  'DeepSeek V4 Pro': 'deepseek-v4-pro',
  'GPT-4o': 'gpt-4o',
  'GPT-5.3 Codex': 'gpt-5.3-codex',
  'GPT-5.2 Codex': 'gpt-5.2-codex',
  'GPT-5.3': 'gpt-5.3',
  'GPT-5.6': 'gpt-5.6',
  'GPT-5.6 mini': 'gpt-5.6-mini',
  'Claude Sonnet 4.5': 'claude-sonnet-4-5',
  'Claude Opus 4.5': 'claude-opus-4-5',
  'Gemini 3 Pro': 'gemini-3-pro',
  'Gemini 3 Flash': 'gemini-3-flash',
  'Llama 4 · 405B': 'llama4:405b',
  'Qwen 3 · 235B': 'qwen3:235b'
};

export function modelId(label: string): string {
  return MODEL_IDS[label] ?? label;
}

/** Map OpenNavo provider cards onto Hermes' canonical provider slugs. */
export function hermesProviderSlug(provider: HermesProvider): string {
  switch (provider) {
    case 'openai':
      return 'openai-api';
    case 'google':
      return 'gemini';
    case 'codex':
      return 'openai-codex';
    case 'claude-code':
      return 'anthropic';
    case 'opencode':
      return 'opencode-zen';
    case 'local':
    case 'custom':
      return '';
    default:
      return provider;
  }
}

/** Official Hermes auxiliary task slots exposed by the desktop GUI. */
export const HERMES_AUX_TASKS = [
  'vision',
  'web_extract',
  'compression',
  'skills_hub',
  'approval',
  'mcp',
  'title_generation',
  'triage_specifier',
  'kanban_decomposer',
  'profile_describer',
  'curator'
] as const;

export type HermesAuxTask = (typeof HERMES_AUX_TASKS)[number];

export interface HermesAuxAssignment {
  task: HermesAuxTask;
  provider: string;
  model: string;
}

export const HERMES_AUX_AUTO: HermesAuxAssignment = { task: 'vision', provider: 'auto', model: '' };
