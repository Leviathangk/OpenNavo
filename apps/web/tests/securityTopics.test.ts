import { describe, expect, it } from 'vitest';
import enUS from '../i18n/locales/en-US.json';
import zhCN from '../i18n/locales/zh-CN.json';
import { SECURITY_TOPICS, TOPIC_LINKS, TOPICS, topicMessageKeys } from '../app/utils/securityTopics';

type Messages = Record<string, unknown>;

function lookup(messages: Messages, key: string): unknown {
  return key.split('.').reduce<unknown>((node, part) => (node as Messages | undefined)?.[part], messages);
}

function strings(node: unknown): string[] {
  if (typeof node === 'string') return [node];
  if (node && typeof node === 'object') return Object.values(node).flatMap(strings);
  return [];
}

const SOURCES = { 'zh-CN': zhCN as Messages, 'en-US': enUS as Messages };

describe('Security topic pages (F8)', () => {
  it('has every message a topic page reads in both source languages', () => {
    for (const topic of SECURITY_TOPICS)
      for (const [code, messages] of Object.entries(SOURCES))
        for (const key of topicMessageKeys(topic))
          expect(typeof lookup(messages, key), `${code} ${key}`).toBe('string');
  });

  it('has the cross-link, related app and page-wide messages', () => {
    const keys = [
      'securityTopics.label',
      'securityTopics.overview',
      'securityTopics.faqTitle',
      'securityTopics.linksTitle',
      'securityTopics.apps.title',
      'securityTopics.apps.body',
      ...TOPIC_LINKS.flatMap(link => [
        `securityTopics.links.${link.key}.title`,
        `securityTopics.links.${link.key}.body`
      ])
    ];
    for (const [code, messages] of Object.entries(SOURCES))
      for (const key of keys) expect(typeof lookup(messages, key), `${code} ${key}`).toBe('string');
  });

  it('writes a title and description of its own for each topic and language', () => {
    for (const messages of Object.values(SOURCES)) {
      const titles = SECURITY_TOPICS.map(topic => lookup(messages, `securityTopics.${topic}.metaTitle`));
      const descriptions = SECURITY_TOPICS.map(topic => lookup(messages, `securityTopics.${topic}.description`));
      expect(new Set(titles).size).toBe(SECURITY_TOPICS.length);
      expect(new Set(descriptions).size).toBe(SECURITY_TOPICS.length);
      for (const title of titles) expect(String(title)).toMatch(/ · OpenNavo$/);
    }
  });

  it('links related apps by their catalog tokens only', () => {
    for (const topic of SECURITY_TOPICS)
      for (const app of TOPICS[topic].apps) expect(app.token).toMatch(/^[a-z0-9][a-z0-9@._+-]{0,127}$/);
  });
});

// No absolute promises (plan F1, F5, F8): nothing is certified safe, protected 100%, or safe to delete.
const BANNED = [
  /100\s?%/,
  /certif/i,
  /guarantee/i,
  /safe to (delete|remove)/i,
  /completely (remove|clean|protect)/i,
  /full(y)? protect/i,
  /认证/,
  /彻底/,
  /绝对/,
  /保证/,
  /放心删除/,
  /可以安全(删除|移除)/,
  /完全(防护|保护|清除)/
];

describe('Claims in the new security copy', () => {
  it('makes no absolute safety promise in either source language', () => {
    for (const [code, messages] of Object.entries(SOURCES))
      for (const namespace of ['securityTopics', 'backgroundItems', 'troubleshooting'])
        for (const text of strings(messages[namespace]))
          for (const pattern of BANNED) expect(pattern.test(text), `${code} ${namespace}: ${text}`).toBe(false);
  });

  it('keeps vue-i18n syntax characters out of the copy', () => {
    // A bare | would split a message into plural forms and @ starts a linked message.
    for (const [code, messages] of Object.entries(SOURCES))
      for (const namespace of ['securityTopics', 'backgroundItems'])
        for (const text of strings(messages[namespace])) {
          expect(text.includes('@'), `${code} ${text}`).toBe(false);
          if (!/^\{total\} label \| \{total\} labels$/.test(text) && !text.includes(' | Declared in'))
            expect(text.includes('|'), `${code} ${text}`).toBe(false);
        }
  });
});
