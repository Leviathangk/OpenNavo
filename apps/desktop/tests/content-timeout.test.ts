import { afterEach, describe, expect, it, vi } from 'vitest';
import { api } from '@/api';
import { unwrap } from '@opennavo/api';

afterEach(() => {
  vi.useRealTimers();
  vi.unstubAllGlobals();
});

describe('Content request timeout', () => {
  it('Unresponsive requests finish after fifteen seconds and allow immediate retry', async () => {
    vi.useFakeTimers();
    const fetcher = vi
      .fn()
      .mockImplementationOnce(
        (_input: unknown, init: RequestInit) =>
          new Promise((_resolve, reject) => {
            init.signal?.addEventListener('abort', () => reject(init.signal?.reason), {
              once: true
            });
          })
      )
      .mockResolvedValueOnce(
        new Response(
          JSON.stringify({
            code: '0000',
            msg: '',
            data: { features: [], popularApps: [], recentlyUpdated: [], collections: [] }
          }),
          { headers: { 'Content-Type': 'application/json' } }
        )
      );
    vi.stubGlobal('fetch', fetcher);
    const request = unwrap(api.GET('/home'));
    const failed = expect(request).rejects.toThrow('Request timed out');
    await vi.advanceTimersByTimeAsync(15_000);
    await failed;
    expect((await unwrap(api.GET('/home'))).popularApps).toEqual([]);
    expect(fetcher).toHaveBeenCalledTimes(2);
  });
});

it('Stalled bodies time out after fifteen seconds despite received headers, releasing reads for retry', async () => {
  vi.useFakeTimers();
  const cancel = vi.fn();
  const stalled = new Response(
    new ReadableStream<Uint8Array>({
      start(controller) {
        controller.enqueue(new TextEncoder().encode('{"code":"0000",'));
      },
      cancel
    }),
    { headers: { 'Content-Type': 'application/json' } }
  );
  const fetcher = vi
    .fn()
    .mockResolvedValueOnce(stalled)
    .mockResolvedValueOnce(
      new Response(
        JSON.stringify({
          code: '0000',
          msg: '',
          data: { features: [], popularApps: [], recentlyUpdated: [], collections: [] }
        }),
        { headers: { 'Content-Type': 'application/json' } }
      )
    );
  vi.stubGlobal('fetch', fetcher);
  const failed = expect(unwrap(api.GET('/home'))).rejects.toThrow('Request timed out');
  await vi.advanceTimersByTimeAsync(15_000);
  await failed;
  await vi.advanceTimersByTimeAsync(0);
  expect(cancel).toHaveBeenCalledTimes(1);
  expect((await unwrap(api.GET('/home'))).popularApps).toEqual([]);
  expect(vi.getTimerCount()).toBe(0);
});

it('Complete reads preserve response status/request ID and clear timers', async () => {
  vi.useFakeTimers();
  const response = new Response('{"code":"0000","msg":"","data":null}', {
    status: 404,
    statusText: 'Not Found',
    headers: { 'Content-Type': 'application/json', 'X-Request-Id': 'body-test' }
  });
  vi.stubGlobal('fetch', vi.fn().mockResolvedValue(response));
  const result = await api.GET('/home');
  expect(result.response).toBe(response);
  expect(result.response.status).toBe(404);
  expect(result.response.headers.get('X-Request-Id')).toBe('body-test');
  expect(vi.getTimerCount()).toBe(0);
});

it('Caller cancellation promptly releases stalled reads without awaiting overall timeout', async () => {
  vi.useFakeTimers();
  const cancel = vi.fn();
  const controller = new AbortController();
  vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response(new ReadableStream<Uint8Array>({ cancel }))));
  const failed = expect(unwrap(api.GET('/home', { signal: controller.signal }))).rejects.toThrow('caller cancelled');
  await vi.advanceTimersByTimeAsync(1);
  controller.abort(new Error('caller cancelled'));
  await failed;
  await vi.advanceTimersByTimeAsync(0);
  expect(cancel).toHaveBeenCalledTimes(1);
  expect(vi.getTimerCount()).toBe(0);
});
