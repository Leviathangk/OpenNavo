import { mount } from '@vue/test-utils';
import { afterEach, describe, expect, it, vi } from 'vitest';
import HermesCopyButton from '@/components/hermes/HermesCopyButton.vue';
import { i18n } from '@/i18n';

function renderCopy(text = 'Hermes answer') {
  return mount(HermesCopyButton, { props: { text }, global: { plugins: [i18n] } });
}

afterEach(() => {
  vi.useRealTimers();
  Reflect.deleteProperty(navigator, 'clipboard');
});

describe('Hermes copy button', () => {
  it('copies the message and confirms before returning to Copy', async () => {
    vi.useFakeTimers();
    const writeText = vi.fn().mockResolvedValue(undefined);
    Object.defineProperty(navigator, 'clipboard', { configurable: true, value: { writeText } });

    const wrapper = renderCopy();
    expect(wrapper.text()).toBe('Copy');

    await wrapper.get('button').trigger('click');
    await Promise.resolve();
    await Promise.resolve();
    await wrapper.vm.$nextTick();

    expect(writeText).toHaveBeenCalledWith('Hermes answer');
    expect(wrapper.text()).toBe('Copied');

    vi.advanceTimersByTime(1500);
    await wrapper.vm.$nextTick();
    expect(wrapper.text()).toBe('Copy');
  });
});
