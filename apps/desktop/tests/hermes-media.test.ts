import { describe, expect, it } from 'vitest';
import { hasHermesMedia, isExternalMediaSrc, mediaKind, mediaName, parseHermesMedia } from '@/hermes/media';

describe('Hermes MEDIA tags', () => {
  it('splits a standalone image tag out of the prose', () => {
    const parts = parseHermesMedia('Here is the image.\nMEDIA:/home/kaka/.hermes/cache/scratch/sample-scene.png');
    expect(parts).toEqual([
      { type: 'text', text: 'Here is the image.' },
      {
        type: 'media',
        path: '/home/kaka/.hermes/cache/scratch/sample-scene.png',
        kind: 'image',
        name: 'sample-scene.png'
      }
    ]);
  });

  it('keeps spaces inside an unquoted path', () => {
    const parts = parseHermesMedia('Rendered MEDIA:/tmp/my render.mp4 now.');
    expect(parts.map(part => part.type)).toEqual(['text', 'media', 'text']);
    expect(parts[1]).toMatchObject({ path: '/tmp/my render.mp4', kind: 'video' });
  });

  it('unwraps quoted and backtick-wrapped paths', () => {
    expect(parseHermesMedia("MEDIA:'/tmp/stop!.md'")[0]).toMatchObject({
      path: '/tmp/stop!.md',
      kind: 'file'
    });
    expect(parseHermesMedia('MEDIA:`/tmp/voice over.wav`')[0]).toMatchObject({
      path: '/tmp/voice over.wav',
      kind: 'audio'
    });
  });

  it('keeps odd quoted names and dotfiles', () => {
    expect(parseHermesMedia('MEDIA:"/tmp/a b.md."')[0]).toMatchObject({
      path: '/tmp/a b.md.',
      kind: 'file',
      name: 'a b.md.'
    });
    expect(parseHermesMedia('MEDIA:/tmp/.env')[0]).toMatchObject({
      path: '/tmp/.env',
      kind: 'file',
      name: '.env'
    });
    expect(parseHermesMedia('MEDIA:/tmp/.gitignore')[0]).toMatchObject({
      path: '/tmp/.gitignore',
      kind: 'file',
      name: '.gitignore'
    });
    expect(parseHermesMedia('MEDIA:/tmp/.png')[0]).toMatchObject({
      path: '/tmp/.png',
      kind: 'file'
    });
    expect(parseHermesMedia('MEDIA:/tmp/.mp4')[0]).toMatchObject({
      path: '/tmp/.mp4',
      kind: 'file'
    });
    expect(parseHermesMedia('MEDIA:"/tmp/.png"')[0]).toMatchObject({
      path: '/tmp/.png',
      kind: 'file'
    });
  });

  it('leaves sentence punctuation outside the path', () => {
    const parts = parseHermesMedia('Look MEDIA:/tmp/a.png. Done.');
    expect(parts[1]).toMatchObject({ path: '/tmp/a.png', kind: 'image' });
    expect(parts[2]).toEqual({ type: 'text', text: '. Done.' });
  });

  it('supports Windows drive paths', () => {
    const parts = parseHermesMedia(String.raw`MEDIA:C:\Users\me\clips\demo.mp4`);
    expect(parts[0]).toMatchObject({
      path: String.raw`C:\Users\me\clips\demo.mp4`,
      kind: 'video',
      name: 'demo.mp4'
    });
  });

  it('renders several tags in order', () => {
    const parts = parseHermesMedia(
      ['Results:', 'MEDIA:/tmp/one.png', 'MEDIA:/tmp/two.mov', 'MEDIA:/tmp/readme.xyz'].join('\n')
    );
    expect(parts.filter(part => part.type === 'media').map(part => part.kind)).toEqual(['image', 'video', 'file']);
  });

  it('keeps a streamed, extension-less path as text', () => {
    expect(parseHermesMedia('MEDIA:/home/kaka/.hermes/cache/scratch/sample-scene')).toEqual([
      { type: 'text', text: 'MEDIA:/home/kaka/.hermes/cache/scratch/sample-scene' }
    ]);
    expect(hasHermesMedia('MEDIA:/home/kaka/.hermes/cache/scratch/sample-scene')).toBe(false);
  });

  it('keeps a streamed, partial known extension as text', () => {
    expect(parseHermesMedia('MEDIA:/tmp/sample-scene.p', true)).toEqual([
      { type: 'text', text: 'MEDIA:/tmp/sample-scene.p' }
    ]);
    expect(parseHermesMedia('MEDIA:/tmp/sample-scene.pn', true)).toEqual([
      { type: 'text', text: 'MEDIA:/tmp/sample-scene.pn' }
    ]);
    expect(parseHermesMedia('MEDIA:/tmp/sample-scene.mp', true)).toEqual([
      { type: 'text', text: 'MEDIA:/tmp/sample-scene.mp' }
    ]);
    expect(parseHermesMedia('MEDIA:/tmp/sample-scene.png', true)[0]).toMatchObject({
      type: 'media',
      path: '/tmp/sample-scene.png',
      kind: 'image'
    });
    expect(hasHermesMedia('MEDIA:/tmp/main.c', true)).toBe(false);
  });

  it('releases a complete token once the turn is no longer streaming', () => {
    expect(parseHermesMedia('MEDIA:/tmp/main.c')[0]).toMatchObject({
      type: 'media',
      path: '/tmp/main.c',
      kind: 'file'
    });
    expect(parseHermesMedia('MEDIA:/tmp/widget.h')[0]).toMatchObject({
      type: 'media',
      path: '/tmp/widget.h',
      kind: 'file'
    });
    expect(hasHermesMedia('MEDIA:/tmp/notes.p')).toBe(true);
    expect(parseHermesMedia('MEDIA:/tmp/main.png')[0]).toMatchObject({
      type: 'media',
      path: '/tmp/main.png',
      kind: 'image'
    });
  });

  it('matches known extensions regardless of case, including paths with spaces', () => {
    expect(parseHermesMedia('MEDIA:/tmp/my file.PNG')[0]).toMatchObject({
      path: '/tmp/my file.PNG',
      kind: 'image'
    });
    expect(parseHermesMedia('MEDIA:/tmp/SHOUT.MOV')[0]).toMatchObject({
      path: '/tmp/SHOUT.MOV',
      kind: 'video'
    });
  });

  it('treats a boundary after a partial suffix as a complete token', () => {
    expect(parseHermesMedia('MEDIA:/tmp/sample-scene.p ')[0]).toMatchObject({
      path: '/tmp/sample-scene.p',
      kind: 'file'
    });
    expect(parseHermesMedia('MEDIA:/tmp/sample-scene.p`')[0]).toMatchObject({
      path: '/tmp/sample-scene.p',
      kind: 'file'
    });
  });

  it('keeps a malformed tag as text', () => {
    expect(parseHermesMedia('MEDIA:')).toEqual([{ type: 'text', text: 'MEDIA:' }]);
    expect(parseHermesMedia('MEDIA:not a path')).toEqual([{ type: 'text', text: 'MEDIA:not a path' }]);
    expect(parseHermesMedia('media:/tmp/not-a-tag.png')).toEqual([{ type: 'text', text: 'media:/tmp/not-a-tag.png' }]);
  });

  it('does not turn examples in code fences or blockquotes into media', () => {
    for (const text of [
      'Example:\n```md\nMEDIA:/tmp/a.png\n```',
      'Example:\n~~~md\nMEDIA:/tmp/a.png\n~~~',
      '> MEDIA:/tmp/a.png'
    ]) {
      expect(parseHermesMedia(text)).toEqual([{ type: 'text', text }]);
      expect(hasHermesMedia(text)).toBe(false);
    }
  });

  it('keeps an inline code sample as text but delivers an exact inline tag', () => {
    const sample = 'Use `MEDIA:/tmp/a.png as an example` in a document.';
    expect(parseHermesMedia(sample)).toEqual([{ type: 'text', text: sample }]);
    expect(hasHermesMedia(sample)).toBe(false);

    expect(parseHermesMedia('Use `MEDIA:/tmp/a.png` in a document.')).toEqual([
      { type: 'text', text: 'Use ' },
      { type: 'media', path: '/tmp/a.png', kind: 'image', name: 'a.png' },
      { type: 'text', text: ' in a document.' }
    ]);
  });

  it('does not re-deliver MEDIA tags stored in JSON string values', () => {
    const text = '{"result":"MEDIA:/tmp/stale.png","ok":true}';
    expect(parseHermesMedia(text)).toEqual([{ type: 'text', text }]);
    const spaced = '{"result": "MEDIA:/tmp/stale.png", "ok": true}';
    expect(parseHermesMedia(spaced)).toEqual([{ type: 'text', text: spaced }]);
    expect(parseHermesMedia(`${text}\nMEDIA:/tmp/fresh.png`).at(-1)).toMatchObject({
      type: 'media',
      path: '/tmp/fresh.png'
    });
  });

  it('stops a path at Chinese punctuation and keeps the punctuation as prose', () => {
    const parts = parseHermesMedia('MEDIA:/tmp/早报.pdf（782.6 KB）');
    expect(parts[0]).toMatchObject({
      type: 'media',
      path: '/tmp/早报.pdf',
      kind: 'file'
    });
    expect(parts.slice(1)).toEqual([{ type: 'text', text: '（782.6 KB）' }]);

    for (const suffix of ['：内容', '。', '，见附件', '；']) {
      const parsed = parseHermesMedia(`MEDIA:/tmp/早报.pdf${suffix}`);
      expect(parsed[0]).toMatchObject({ type: 'media', path: '/tmp/早报.pdf' });
      expect(parsed.slice(1)).toEqual([{ type: 'text', text: suffix }]);
    }
  });

  it('classifies URLs and known extensions', () => {
    expect(mediaKind('https://example.com/render.webp?size=2')).toBe('image');
    expect(mediaKind('/tmp/clip.MOV')).toBe('video');
    expect(mediaKind('/tmp/voice.opus')).toBe('audio');
    expect(mediaKind('/tmp/bundle.apk')).toBe('file');
    expect(mediaName(String.raw`C:\Users\me\a b.pdf`)).toBe('a b.pdf');
    expect(mediaName('https://example.com/a%20b.png')).toBe('a b.png');
  });

  it('separates web URLs from local paths and data URLs', () => {
    expect(isExternalMediaSrc('https://example.com/a.png')).toBe(true);
    expect(isExternalMediaSrc('http://100.64.0.9/a.mp4')).toBe(true);
    expect(isExternalMediaSrc('data:image/png;base64,AAAA')).toBe(false);
    expect(isExternalMediaSrc('/tmp/a.png')).toBe(false);
  });
});
