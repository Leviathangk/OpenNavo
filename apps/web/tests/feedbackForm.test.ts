import { describe, expect, it } from 'vitest';
import {
  FEEDBACK_LIMITS,
  canSend,
  feedbackBody,
  feedbackProblems,
  isAcceptedLink,
  showsProblemFields,
  type FeedbackDraft
} from '../app/utils/feedbackForm';

function draft(extra: Partial<FeedbackDraft> = {}): FeedbackDraft {
  return {
    type: 'install_failed',
    pkg: { kind: 'cask', token: 'zed' },
    content: 'The installer stops halfway.',
    contact: '',
    website: '',
    packageVersion: '',
    macosVersion: '',
    symptom: '',
    diagnosticLog: '',
    links: [''],
    ...extra
  };
}

describe('Links the API keeps', () => {
  it('accepts https addresses with a host name and nothing after ? or #', () => {
    expect(isAcceptedLink('https://github.com/zed-industries/zed/issues/123')).toBe(true);
    expect(isAcceptedLink('https://discussions.apple.com/thread/255')).toBe(true);
    for (const bad of [
      'http://example.com/a',
      'https://example.com/a?b=1',
      'https://example.com/a#top',
      'https://user:secret@example.com/',
      'https://localhost/a',
      'https://127.0.0.1/a',
      'https://[::1]/a',
      'ftp://example.com/a',
      'example.com/a',
      'https://exa mple.com/',
      `https://example.com/${'a'.repeat(FEEDBACK_LIMITS.linkLength)}`
    ])
      expect(isAcceptedLink(bad)).toBe(false);
  });
});

describe('Feedback problems', () => {
  it('lets a plain report through', () => {
    expect(canSend(feedbackProblems(draft()))).toBe(true);
  });

  it('checks the text length in characters, as the API counts them', () => {
    expect(feedbackProblems(draft({ content: '短' })).fields.content).toBe('length');
    expect(canSend(feedbackProblems(draft({ content: '五个汉字呢' })))).toBe(true);
    expect(feedbackProblems(draft({ content: 'x'.repeat(2001) })).fields.content).toBe('length');
  });

  it('keeps addresses in the text as plain text, so a broken download link can be reported', () => {
    expect(canSend(feedbackProblems(draft({ content: 'Broken: http://example.com/download?id=1#top' })))).toBe(true);
  });

  it('allows three link fields, not counting addresses in the text', () => {
    const links = ['https://a.example/1', 'https://b.example/2', 'https://c.example/3'];
    expect(canSend(feedbackProblems(draft({ links, content: 'Also https://d.example/4 here' })))).toBe(true);
    const over = feedbackProblems(draft({ links: [...links, 'https://d.example/4'] }));
    expect(over.tooManyLinks).toBe(true);
    expect(canSend(over)).toBe(false);
    const invalid = feedbackProblems(draft({ links: ['https://a.example/1', 'not a link'] }));
    expect(invalid.links).toEqual([true, false]);
    expect(canSend(invalid)).toBe(false);
  });

  it('limits versions and symptom in characters, and the log in UTF-8 bytes', () => {
    expect(feedbackProblems(draft({ packageVersion: '1'.repeat(101) })).fields.packageVersion).toBe('length');
    expect(feedbackProblems(draft({ macosVersion: '1'.repeat(101) })).fields.macosVersion).toBe('length');
    expect(feedbackProblems(draft({ symptom: '闪'.repeat(500) })).fields.symptom).toBeUndefined();
    expect(feedbackProblems(draft({ symptom: '闪'.repeat(501) })).fields.symptom).toBe('length');
    expect(feedbackProblems(draft({ diagnosticLog: 'a'.repeat(16384) })).fields.diagnosticLog).toBeUndefined();
    // 5462 three-byte characters are 16386 bytes: over the limit though under 16384 characters.
    expect(feedbackProblems(draft({ diagnosticLog: '日'.repeat(5462) })).fields.diagnosticLog).toBe('size');
  });

  it('ignores the problem fields for reports that do not show them', () => {
    expect(showsProblemFields('install_failed')).toBe(true);
    expect(showsProblemFields('other')).toBe(true);
    expect(showsProblemFields('suggest_package')).toBe(false);
    expect(canSend(feedbackProblems(draft({ type: 'suggest_package', symptom: 'x'.repeat(600) })))).toBe(true);
  });
});

describe('Feedback request body', () => {
  it('sends only what was filled in, without nulls', () => {
    expect(feedbackBody(draft({ type: 'suggest_package', pkg: null, content: '  Please list Zed.  ' }))).toEqual({
      type: 'suggest_package',
      content: 'Please list Zed.',
      platform: 'web',
      website: ''
    });
  });

  it('carries the problem fields and links for a problem report', () => {
    expect(
      feedbackBody(
        draft({
          contact: ' me@example.com ',
          packageVersion: ' 0.150.2 ',
          macosVersion: '15.1',
          symptom: 'Quits on launch',
          diagnosticLog: 'Crashed Thread: 0\n',
          links: ['https://github.com/zed-industries/zed/issues/1', ' ']
        })
      )
    ).toEqual({
      type: 'install_failed',
      packageKind: 'cask',
      packageToken: 'zed',
      content: 'The installer stops halfway.',
      contact: 'me@example.com',
      packageVersion: '0.150.2',
      macosVersion: '15.1',
      symptom: 'Quits on launch',
      diagnosticLog: 'Crashed Thread: 0',
      links: ['https://github.com/zed-industries/zed/issues/1'],
      platform: 'web',
      website: ''
    });
  });

  it('keeps what was typed in hidden fields out of the request', () => {
    const body = feedbackBody(draft({ type: 'wrong_info', symptom: 'typed before switching type' }));
    expect(body).not.toHaveProperty('symptom');
  });
});
