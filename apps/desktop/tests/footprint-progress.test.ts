import { mount } from '@vue/test-utils';
import { describe, expect, it } from 'vitest';
import FootprintScanProgress from '@/components/installed/FootprintScanProgress.vue';
import { i18n } from '@/i18n';

describe('Native footprint progress', () => {
  it('Keeps retries unfinished and reaches 100% only after every app has a terminal result', async () => {
    i18n.global.locale.value = 'zh-CN';
    const wrapper = mount(FootprintScanProgress, {
      props: { progress: { total: 10, completed: 8, failed: 0, retrying: 2, running: true } },
      global: { plugins: [i18n] }
    });
    expect(wrapper.get('[role="progressbar"]').attributes('aria-valuenow')).toBe('80');
    expect(wrapper.text()).toContain('已完成 8 / 10');
    expect(wrapper.text()).toContain('等待重试 2 个');
    expect(wrapper.text()).not.toContain('统计完成');
    await wrapper.setProps({
      progress: { total: 10, completed: 10, failed: 1, retrying: 0, running: false }
    });
    expect(wrapper.get('[role="progressbar"]').attributes('aria-valuenow')).toBe('100');
    expect(wrapper.text()).toContain('统计完成，部分应用失败');
    expect(wrapper.text()).toContain('1 个应用失败');
    expect(wrapper.find('.footprint-scan-spinner').exists()).toBe(false);
    wrapper.unmount();
  });
});
