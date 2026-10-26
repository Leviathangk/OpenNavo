import { robotsAllowsIndexing } from '../app/utils/indexing';
import { describe, expect, it } from 'vitest';
import type { PackageDetail } from '@opennavo/api';
import {
  TROUBLESHOOTING_MIN_ISSUES,
  hasTroubleshootingPage,
  issueAnchor,
  troubleshootingDecision,
  troubleshootingPath,
  verifiedIssues,
  type KnownIssueFact
} from '../app/utils/troubleshooting';

type Facts = NonNullable<PackageDetail['facts']>;
type Indexing = NonNullable<PackageDetail['indexing']>;

function issue(title: string, extra: Partial<KnownIssueFact> = {}): KnownIssueFact {
  return {
    locale: 'en-US',
    title,
    symptom: 'The app quits right after opening.',
    appVersions: ['2.1'],
    macosVersions: ['15.0'],
    steps: ['Update to 2.1.1.'],
    communityLinks: [],
    evidence: {
      sourceUrl: 'https://vendor.example/support',
      checkedAt: '2026-10-18T08:00:00Z',
      validUntil: null,
      status: 'verified'
    },
    ...extra
  };
}

const decision = (robots: string) => ({
  robots,
  canonicalPath: '',
  hreflang: [],
  inSitemap: robotsAllowsIndexing(robots),
  announce: false,
  reason: ''
});

function app(issues: KnownIssueFact[], overview: string = 'index, follow') {
  const indexing = {
    indexable: robotsAllowsIndexing(overview),
    reason: null,
    locales: [],
    enforced: true,
    notesCount: 0,
    pages: {
      overview: decision(overview),
      versions: decision('noindex, follow'),
      dependencies: decision('noindex, follow'),
      details: decision('noindex, follow')
    }
  } as Indexing;
  return { facts: { issues } as Facts, indexing };
}

const three = (extra: Partial<KnownIssueFact> = {}) => ['A', 'B', 'C'].map(title => issue(title, extra));

describe('Known issues a page may state', () => {
  it('keeps verified issues with versions, steps, a source and a check date', () => {
    const issues = [
      issue('kept'),
      issue('stale', { evidence: { ...issue('x').evidence, status: 'stale' } }),
      issue('no date', { evidence: { ...issue('x').evidence, checkedAt: null } }),
      issue('no source', { evidence: { ...issue('x').evidence, sourceUrl: '' } }),
      issue('no app version', { appVersions: [] }),
      issue('no macOS version', { macosVersions: [] }),
      issue('no steps', { steps: [] })
    ];
    expect(verifiedIssues(app(issues)).map(entry => entry.title)).toEqual(['kept']);
    expect(verifiedIssues(null)).toEqual([]);
    expect(verifiedIssues({ facts: undefined })).toEqual([]);
  });

  it('opens the page only with enough verified issues, so it is never empty or thin', () => {
    expect(TROUBLESHOOTING_MIN_ISSUES).toBe(3);
    expect(hasTroubleshootingPage(app(three().slice(0, 2)))).toBe(false);
    expect(hasTroubleshootingPage(app(three()))).toBe(true);
    // Unverified issues never count toward the threshold.
    expect(
      hasTroubleshootingPage(
        app([...three().slice(0, 2), issue('D', { evidence: { ...issue('x').evidence, status: 'needs_review' } })])
      )
    ).toBe(false);
  });

  it('has stable paths and anchors', () => {
    expect(troubleshootingPath('visual-studio-code')).toBe('/apps/visual-studio-code/troubleshooting');
    expect(issueAnchor(0)).toBe('issue-1');
  });
});

describe('Troubleshooting page decision', () => {
  it('is indexable only when the overview is and every issue is in the page language', () => {
    expect(troubleshootingDecision(app(three()), 'en-US')).toEqual({
      robots: 'index, follow',
      canonicalPath: '',
      hreflang: ['en-US'],
      inSitemap: false,
      announce: false,
      reason: 'troubleshooting'
    });
    // English issues on a Chinese page: shown, but not indexed.
    expect(troubleshootingDecision(app(three()), 'zh-CN')).toMatchObject({
      robots: 'noindex, follow',
      hreflang: [],
      reason: 'locale_not_ready'
    });
    expect(troubleshootingDecision(app(three({ locale: 'zh-CN' })), 'zh-CN').robots).toBe('index, follow');
    expect(troubleshootingDecision(app(three(), 'noindex, follow'), 'en-US')).toMatchObject({
      robots: 'noindex, follow',
      reason: 'overview_not_indexable'
    });
    expect(troubleshootingDecision(app(three().slice(0, 1)), 'en-US').reason).toBe('troubleshooting_thin');
    // The server's decision, once the API has one for this page, is used as it is (S5).
    const server = {
      robots: 'noindex, follow',
      canonicalPath: '/apps/fixture',
      hreflang: [],
      inSitemap: false,
      announce: false,
      reason: 'evidence_stale'
    };
    const withServer = {
      ...app(three()),
      indexing: { ...app(three()).indexing, pages: { ...app(three()).indexing.pages, troubleshooting: server } }
    };
    expect(troubleshootingDecision(withServer as never, 'en-US')).toEqual(server);
  });
});

it('legacy troubleshooting accepts preview directives but respects noindex', () => {
  for (const [robots, expected] of [
    ['index, follow, max-image-preview:large, max-snippet:-1, max-video-preview:-1', 'index, follow'],
    ['index, noindex, follow', 'noindex, follow'],
    ['follow, max-snippet:-1', 'noindex, follow']
  ] as const)
    expect(troubleshootingDecision(app(three(), robots), 'en-US').robots).toBe(expected);
});
