// Feedback form (05 §4, F7), pure for testing: the request body and the same limits the API applies
// (store.SaveFeedback and facts.FeedbackLinks), so the form explains a problem before sending instead of
// failing with a generic error. Submissions stay private; nothing here is ever published.
import type { PublicComponents } from '@opennavo/api';
import type { PackageKind } from '@opennavo/shared';

type FeedbackRequest = PublicComponents['schemas']['FeedbackCreateRequest'];
export type FeedbackType = FeedbackRequest['type'];

export const FEEDBACK_LIMITS = {
  contentMin: 5,
  contentMax: 2000,
  contact: 200,
  /** App and macOS versions. */
  version: 100,
  symptom: 500,
  /** The diagnostic log, in UTF-8 bytes (16 KiB). */
  logBytes: 16384,
  /** Link fields; addresses written in the description are plain text and do not count (F7). */
  links: 3,
  linkLength: 2048
} as const;

/** Reports about an app misbehaving get the version, macOS, symptom and log fields. */
const PROBLEM_TYPES: readonly FeedbackType[] = ['install_failed', 'other'];

export function showsProblemFields(type: FeedbackType): boolean {
  return PROBLEM_TYPES.includes(type);
}

/** Characters as the API counts them (code points), so an emoji or a Chinese character is one. */
export function charLength(text: string): number {
  return Array.from(text).length;
}

const encoder = new TextEncoder();
export function byteLength(text: string): number {
  return encoder.encode(text).length;
}

const IPV4 = /^\d{1,3}(?:\.\d{1,3}){3}$/;

/**
 * A link the API keeps: https with a host name (not localhost or an IP address), no user name or password,
 * no ?query or #fragment, at most 2048 characters. The API never fetches it.
 */
export function isAcceptedLink(raw: string): boolean {
  if (!raw || byteLength(raw) > FEEDBACK_LIMITS.linkLength || /\s/.test(raw)) return false;
  let url: URL;
  try {
    url = new URL(raw);
  } catch {
    return false;
  }
  const host = url.hostname;
  return (
    url.protocol === 'https:' &&
    Boolean(host) &&
    !url.username &&
    !url.password &&
    host !== 'localhost' &&
    !IPV4.test(host) &&
    !host.startsWith('[') &&
    !url.search &&
    !url.hash
  );
}

export interface FeedbackDraft {
  type: FeedbackType;
  pkg: { kind: PackageKind; token: string } | null;
  content: string;
  contact: string;
  /** Honeypot; people never see it. */
  website: string;
  packageVersion: string;
  macosVersion: string;
  symptom: string;
  diagnosticLog: string;
  links: string[];
}

export type FeedbackField = 'content' | 'contact' | 'packageVersion' | 'macosVersion' | 'symptom' | 'diagnosticLog';

/** Why the draft cannot be sent yet, by field, as a key under feedback.errors; empty when it can. */
export interface FeedbackProblems {
  fields: Partial<Record<FeedbackField, 'length' | 'size'>>;
  /** Per link field: whether that address is one the API keeps. */
  links: boolean[];
  /** More than three link fields filled in. */
  tooManyLinks: boolean;
}

const filled = (links: readonly string[]) => links.map(link => link.trim()).filter(Boolean);

export function feedbackProblems(draft: FeedbackDraft): FeedbackProblems {
  const fields: FeedbackProblems['fields'] = {};
  const content = draft.content.trim();
  const length = charLength(content);
  // Addresses in the description are kept as text (a broken download link is a normal report), so only the
  // link fields are checked and counted.
  if (length < FEEDBACK_LIMITS.contentMin || length > FEEDBACK_LIMITS.contentMax) fields.content = 'length';
  if (charLength(draft.contact.trim()) > FEEDBACK_LIMITS.contact) fields.contact = 'length';
  if (showsProblemFields(draft.type)) {
    if (charLength(draft.packageVersion.trim()) > FEEDBACK_LIMITS.version) fields.packageVersion = 'length';
    if (charLength(draft.macosVersion.trim()) > FEEDBACK_LIMITS.version) fields.macosVersion = 'length';
    if (charLength(draft.symptom.trim()) > FEEDBACK_LIMITS.symptom) fields.symptom = 'length';
    if (byteLength(draft.diagnosticLog.trim()) > FEEDBACK_LIMITS.logBytes) fields.diagnosticLog = 'size';
  }
  const links = draft.links.map(link => !link.trim() || isAcceptedLink(link.trim()));
  const tooManyLinks = filled(draft.links).length > FEEDBACK_LIMITS.links;
  return { fields, links, tooManyLinks };
}

export function canSend(problems: FeedbackProblems): boolean {
  return !Object.keys(problems.fields).length && problems.links.every(Boolean) && !problems.tooManyLinks;
}

/**
 * The request: absent fields are left out rather than null, as in the contract's examples, and the problem
 * fields only go with the types that show them (a hidden field keeps what was typed but is not sent).
 */
export function feedbackBody(draft: FeedbackDraft): FeedbackRequest {
  const body: FeedbackRequest = {
    type: draft.type,
    content: draft.content.trim(),
    platform: 'web',
    website: draft.website
  };
  if (draft.pkg) {
    body.packageKind = draft.pkg.kind;
    body.packageToken = draft.pkg.token;
  }
  const contact = draft.contact.trim();
  if (contact) body.contact = contact;
  if (showsProblemFields(draft.type)) {
    const packageVersion = draft.packageVersion.trim();
    const macosVersion = draft.macosVersion.trim();
    const symptom = draft.symptom.trim();
    const diagnosticLog = draft.diagnosticLog.trim();
    if (packageVersion) body.packageVersion = packageVersion;
    if (macosVersion) body.macosVersion = macosVersion;
    if (symptom) body.symptom = symptom;
    if (diagnosticLog) body.diagnosticLog = diagnosticLog;
  }
  const links = filled(draft.links);
  if (links.length) body.links = links;
  return body;
}
