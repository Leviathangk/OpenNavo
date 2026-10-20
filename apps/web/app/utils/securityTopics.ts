// Mac security topics (F8): /security/firewall, /security/clamav and /security/xprotect. Each page is one
// shape filled from securityTopics.<topic> messages; this table says which sections, cards, lists and
// questions a topic has, so the template, the tests and the translations walk the same keys. Copy rules:
// facts the app or Apple's own documentation support, no absolute promise ("100% protection", "certified
// safe"), and other products named only to state facts, never as partners or official versions.

export const SECURITY_TOPICS = ['firewall', 'clamav', 'xprotect'] as const;
export type SecurityTopic = (typeof SECURITY_TOPICS)[number];

export interface TopicSection {
  key: string;
  /** Lead paragraph under the heading (sections.<key>.body). */
  body?: boolean;
  /** Titled cards (sections.<key>.cards.<card>.title / .body). */
  cards?: readonly string[];
  /** A bulleted list (sections.<key>.items.<item>). */
  items?: readonly string[];
  /** Numbered steps (sections.<key>.steps.<step>). */
  steps?: readonly string[];
  /** A closing note in smaller type (sections.<key>.note). */
  note?: boolean;
}

export interface TopicConfig {
  icon: string;
  sections: readonly TopicSection[];
  faq: readonly string[];
  /** Related catalog apps, linked only when the catalog has them (names are the products' own). */
  apps: readonly { token: string; name: string }[];
}

export const TOPICS: Record<SecurityTopic, TopicConfig> = {
  firewall: {
    icon: 'brick-wall-fire',
    sections: [
      { key: 'what', body: true, cards: ['ask', 'rules', 'blocklists', 'modes'] },
      { key: 'how', body: true, note: true },
      { key: 'limits', items: ['incoming', 'notRunning', 'directIp', 'release'] },
      { key: 'origin', body: true }
    ],
    faq: ['builtIn', 'free', 'breakApps', 'privacy', 'remove'],
    apps: [
      { token: 'lulu', name: 'LuLu' },
      { token: 'little-snitch', name: 'Little Snitch' }
    ]
  },
  clamav: {
    icon: 'scan-search',
    sections: [
      { key: 'what', body: true },
      { key: 'how', cards: ['database', 'scans', 'install', 'quarantine', 'resources'] },
      { key: 'limits', items: ['coverage', 'signatures', 'realtime', 'large'] },
      { key: 'extras', body: true, cards: ['signatures', 'startup', 'posture', 'virusTotal'] }
    ],
    faq: ['xprotect', 'free', 'upload', 'terminal'],
    apps: [
      { token: 'clamxav', name: 'ClamXAV' },
      { token: 'malwarebytes', name: 'Malwarebytes' }
    ]
  },
  xprotect: {
    icon: 'shield-check',
    sections: [
      { key: 'what', cards: ['signatures', 'checks', 'remediation', 'gatekeeper'] },
      { key: 'limits', items: ['known', 'updates', 'visibility'] },
      { key: 'check', steps: ['systemInformation', 'terminal', 'settings'], note: true },
      { key: 'extras', body: true, cards: ['scan', 'startup', 'signatures'] }
    ],
    faq: ['enough', 'off', 'where', 'clamav'],
    apps: [
      { token: 'malwarebytes', name: 'Malwarebytes' },
      { token: 'knockknock', name: 'KnockKnock' },
      { token: 'blockblock', name: 'BlockBlock' }
    ]
  }
};

/** Every message key a topic page reads, so tests can check both source languages carry all of them. */
export function topicMessageKeys(topic: SecurityTopic): string[] {
  const base = `securityTopics.${topic}`;
  const config = TOPICS[topic];
  const keys = [
    `${base}.metaTitle`,
    `${base}.description`,
    `${base}.hero.kicker`,
    `${base}.hero.title`,
    `${base}.hero.lede`,
    `${base}.cta.title`,
    `${base}.cta.body`,
    `${base}.breadcrumb`
  ];
  for (const section of config.sections) {
    const prefix = `${base}.sections.${section.key}`;
    keys.push(`${prefix}.title`);
    if (section.body) keys.push(`${prefix}.body`);
    if (section.note) keys.push(`${prefix}.note`);
    for (const card of section.cards ?? []) keys.push(`${prefix}.cards.${card}.title`, `${prefix}.cards.${card}.body`);
    for (const item of section.items ?? []) keys.push(`${prefix}.items.${item}`);
    for (const step of section.steps ?? []) keys.push(`${prefix}.steps.${step}`);
  }
  for (const question of config.faq) keys.push(`${base}.faq.${question}.q`, `${base}.faq.${question}.a`);
  return keys;
}

/** The topic pages beside each other and the background items encyclopedia, for cross-links. */
export const TOPIC_LINKS = [
  { key: 'firewall', path: '/security/firewall', icon: 'brick-wall-fire' },
  { key: 'clamav', path: '/security/clamav', icon: 'scan-search' },
  { key: 'xprotect', path: '/security/xprotect', icon: 'shield-check' },
  { key: 'backgroundItems', path: '/background-items', icon: 'power' }
] as const;
