import { readFileSync, statSync } from 'node:fs';
import { resolve } from 'node:path';
import { describe, expect, it } from 'vitest';

const publicDirectory = resolve(process.cwd(), 'public');
const healthEndpoints = ['api/health', 'readyz'] as const;

describe('static admin health endpoints', () => {
  it.each(healthEndpoints)('publishes a minimal JSON response at /%s', endpoint => {
    const path = resolve(publicDirectory, endpoint);
    expect(statSync(path).isFile()).toBe(true);

    const body = readFileSync(path, 'utf8');
    expect(JSON.parse(body)).toEqual({ status: 'ok' });
    expect(body).not.toMatch(/(?:host|hostname|ip|version|error|internal)/i);
  });
});
