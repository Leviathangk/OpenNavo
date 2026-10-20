import { flushPromises, mount } from '@vue/test-utils';
import { reactive } from 'vue';
import { NCheckbox } from 'naive-ui';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import ClaimEvidence from '../src/views/content/article-detail/modules/claim-evidence.vue';
import type { Schemas } from '../src/typings/api/opennavo';

const api = vi.hoisted(() => ({ captureArticleEvidence: vi.fn(), createArticleTestArtifact: vi.fn() }));
vi.mock('@/service/api', () => api);
vi.mock('@/locales', () => ({ $t: (key: string) => key }));
const label = (key: string) => `page.content.articleDetail.${key}`;
function setup(canPublish = true, prepare = vi.fn().mockResolvedValue(true)) {
  const claim = reactive<Schemas['ArticleClaim']>({
    text: 'We tested export in 10 seconds.',
    kind: 'test',
    evidenceId: 0,
    excerpt: 'We tested export in 10 seconds. Version 1.0.',
    packageId: 7,
    appVersion: '1.0',
    region: 'ZZ'
  });
  const wrapper = mount(ClaimEvidence, {
    props: { articleId: 1, claim, canPublish, prepare, onChange: value => Object.assign(claim, value) }
  });
  const button = (key: string) => wrapper.findAll('button').find(node => node.text() === label(key));
  const fill = async (key: string, value: string) => wrapper.find(`[aria-label="${label(key)}"]`).setValue(value);
  return { wrapper, claim, button, fill, prepare };
}
beforeEach(() => {
  vi.clearAllMocks();
});
describe('Human article evidence workflow', () => {
  it('captures a source and fills its returned ID without a task or manual ID entry', async () => {
    const { claim, button, fill, prepare } = setup();
    api.captureArticleEvidence.mockResolvedValue({
      data: { id: 42, excerpt: claim.excerpt, finalUrl: 'https://vendor.example/guide' },
      error: null
    });
    await fill('sourceUrl', 'https://vendor.example/guide');
    await button('captureSource')!.trigger('click');
    await flushPromises();
    expect(prepare).toHaveBeenCalledOnce();
    expect(api.captureArticleEvidence).toHaveBeenCalledWith(1, {
      url: 'https://vendor.example/guide',
      excerpt: claim.excerpt,
      packageId: 7,
      idempotencyKey: expect.any(String)
    });
    expect(claim.evidenceId).toBe(42);
  });
  it('registers an attested report and attaches the returned ID', async () => {
    const { wrapper, claim, button, fill } = setup();
    api.createArticleTestArtifact.mockResolvedValue({ data: { id: 99 }, error: null });
    await button('registerReport')!.trigger('click');
    for (const [key, value] of Object.entries({
      macOS: '15.0',
      performedAt: '2026-10-19T10:00',
      runner: 'Editor',
      testEnvironment: 'MacBook Pro',
      testCommand: 'Export fixture',
      testOutput: claim.text
    }))
      await fill(key, value);
    expect(button('saveReport')!.attributes('disabled')).toBeDefined();
    wrapper.findComponent(NCheckbox).vm.$emit('update:checked', true);
    await flushPromises();
    await button('saveReport')!.trigger('click');
    await flushPromises();
    expect(api.createArticleTestArtifact).toHaveBeenCalledWith(
      1,
      expect.objectContaining({
        packageId: 7,
        appVersion: '1.0',
        conclusions: [claim.text],
        output: claim.text,
        attested: true
      })
    );
    expect(claim.testArtifactId).toBe(99);
  });
  it('preserves inputs and gives a recovery message after capture failure', async () => {
    const { wrapper, claim, button, fill } = setup(false);
    api.captureArticleEvidence.mockResolvedValue({ data: null, error: new Error('invalid source') });
    await fill('sourceUrl', 'https://vendor.example/guide');
    await button('captureSource')!.trigger('click');
    await flushPromises();
    expect(claim.evidenceId).toBe(0);
    expect(wrapper.text()).toContain(label('captureFailed'));
    expect(button('registerReport')).toBeUndefined();
    expect((wrapper.find(`[aria-label="${label('sourceUrl')}"]`).element as HTMLInputElement).value).toBe(
      'https://vendor.example/guide'
    );
  });
  it('does not capture when linking the app fails', async () => {
    const { button, fill } = setup(true, vi.fn().mockResolvedValue(false));
    await fill('sourceUrl', 'https://vendor.example/guide');
    await button('captureSource')!.trigger('click');
    await flushPromises();
    expect(api.captureArticleEvidence).not.toHaveBeenCalled();
  });
});
