// @vitest-environment node
import { mkdtempSync, mkdirSync, writeFileSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { describe, expect, it } from 'vitest';
import { loadReleaseNotes, RELEASE_NOTE_LOCALES, validateNotes } from '../scripts/release-notes.mjs';
import { releaseBody, TARGETS } from '../scripts/release-lib.mjs';

describe('Versioned release notes', () => {
  it.each([undefined, '', ' \n ', 'OpenNavo 0.3.0', '# OpenNavo 0.3.0', '0.3.0', 'TODO', '待补充'])(
    'rejects absent or placeholder notes: %s',
    notes => {
      expect(() => validateNotes(notes, '0.3.0', 'en-US')).toThrow('real release notes');
    }
  );
  it('reads exact beta version files and preserves multiline Markdown as data', () => {
    const dir = mkdtempSync(join(tmpdir(), 'opennavo-notes-'));
    try {
      const version = '0.3.0-beta.1';
      mkdirSync(join(dir, version));
      const en = '## Fixes\n\n- Fix startup.\n- Preserve "quotes".';
      expect(() => loadReleaseNotes(dir, version)).toThrow();
      writeFileSync(join(dir, version, 'en-US.md'), en);
      expect(() => loadReleaseNotes(dir, version)).toThrow();
      for (const locale of RELEASE_NOTE_LOCALES) writeFileSync(join(dir, version, `${locale}.md`), en);
      rmSync(join(dir, version, 'zh-Hant.md'));
      const notes = loadReleaseNotes(dir, version);
      const body = releaseBody({
        version,
        channel: 'beta',
        notes,
        artifacts: Object.keys(TARGETS).map(target => ({
          target,
          url: 'https://example.test/app',
          bytes: 1,
          sha256: 'a'.repeat(64)
        }))
      });
      expect(JSON.parse(JSON.stringify(body)).i18n).toEqual(
        Object.fromEntries(RELEASE_NOTE_LOCALES.map(locale => [locale, { notes: en }]))
      );
      expect(() =>
        releaseBody({ version, channel: 'beta', artifacts: body.artifacts, notes: { ...notes, 'ja-JP': 'TODO' } })
      ).toThrow('real release notes');
      expect(() => loadReleaseNotes(dir, '0.3.0')).toThrow();
      expect(() => loadReleaseNotes(dir, '../escape')).toThrow('Invalid');
      expect(() => releaseBody({ version, channel: 'beta', artifacts: body.artifacts })).toThrow('real release notes');
    } finally {
      rmSync(dir, { recursive: true, force: true });
    }
  });
});

it('registers Markdown files through the CLI without executing their contents', async () => {
  const { createServer } = await import('node:http');
  const { execFile } = await import('node:child_process');
  const { promisify } = await import('node:util');
  const dir = mkdtempSync(join(tmpdir(), 'opennavo-register-notes-'));
  let received;
  const server = createServer(async (req, res) => {
    let body = '';
    for await (const chunk of req) body += chunk;
    received = JSON.parse(body);
    res.setHeader('Content-Type', 'application/json');
    res.end(JSON.stringify({ code: '0000', data: { id: 1 } }));
  });
  try {
    await new Promise(resolve => server.listen(0, '127.0.0.1', resolve));
    mkdirSync(join(dir, '0.3.0'));
    const notes = '## Fixes\n\n- Preserve `code`, "$HOME" and $(literal).';
    for (const locale of RELEASE_NOTE_LOCALES)
      writeFileSync(join(dir, '0.3.0', `${locale}.md`), locale === 'en-US' ? notes : `${notes}\n- ${locale}`);
    for (const target of Object.keys(TARGETS))
      writeFileSync(
        join(dir, `${target}.json`),
        JSON.stringify({
          target,
          url: 'https://example.test/app',
          bytes: 1,
          sha256: 'a'.repeat(64)
        })
      );
    await promisify(execFile)(
      process.execPath,
      ['scripts/register-release.mjs', '--version', '0.3.0', '--dir', dir, '--notes-dir', dir],
      {
        cwd: new URL('..', import.meta.url),
        env: {
          ...process.env,
          ADMIN_API_BASE: `http://127.0.0.1:${server.address().port}`,
          CI_RELEASE_TOKEN: 'fixture-only'
        }
      }
    );
    expect(received.sourceLocale).toBe('en-US');
    expect(received.i18n).toEqual(
      Object.fromEntries(
        RELEASE_NOTE_LOCALES.map(locale => [
          locale,
          { notes: locale === 'en-US' ? notes : `${notes}\n- ${locale === 'zh-Hant' ? 'zh-CN' : locale}` }
        ])
      )
    );
  } finally {
    await new Promise(resolve => server.close(resolve));
    rmSync(dir, { recursive: true, force: true });
  }
});
