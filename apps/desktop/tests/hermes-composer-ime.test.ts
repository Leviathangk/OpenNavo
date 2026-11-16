import { flushPromises, shallowMount } from '@vue/test-utils';
import type { VueWrapper } from '@vue/test-utils';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import AssistantPage from '@/pages/AssistantPage.vue';
import { i18n } from '@/i18n';
import { useHermesStore } from '@/stores/hermes';
import { setup } from './helpers';

const wrappers: VueWrapper[] = [];

beforeEach(() => {
  vi.stubGlobal(
    'ResizeObserver',
    class {
      observe() {}
      unobserve() {}
      disconnect() {}
    }
  );
  vi.stubGlobal('requestAnimationFrame', (callback: FrameRequestCallback) => {
    callback(0);
    return 1;
  });
  window.history.replaceState(null, '', '/?mock=hermes-ready');
});

afterEach(() => {
  wrappers.splice(0).forEach(wrapper => wrapper.unmount());
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
  window.history.replaceState(null, '', '/');
});

async function composer() {
  const { pinia, router } = await setup('/assistant');
  const wrapper = shallowMount(AssistantPage, {
    global: { plugins: [pinia, router, i18n] },
    attachTo: document.body
  });
  wrappers.push(wrapper);
  await flushPromises();
  const store = useHermesStore();
  const send = vi.spyOn(store, 'send').mockResolvedValue(undefined);
  const textarea = wrapper.get<HTMLTextAreaElement>('#hermes-composer');
  await textarea.setValue("w'd's'd");
  return { send, textarea };
}

function press(target: HTMLTextAreaElement, init: KeyboardEventInit = {}, keyCode = 13) {
  const event = new KeyboardEvent('keydown', {
    key: 'Enter',
    bubbles: true,
    cancelable: true,
    ...init
  });
  Object.defineProperty(event, 'keyCode', { value: keyCode });
  target.dispatchEvent(event);
  return event;
}

describe('Hermes composer Enter handling', () => {
  it('Composes a Chinese candidate instead of sending when WebKit reports keyCode 229', async () => {
    const { send, textarea } = await composer();

    const event = press(textarea.element, {}, 229);
    await flushPromises();

    expect(event.defaultPrevented).toBe(false);
    expect(send).not.toHaveBeenCalled();
    expect(textarea.element.value).toBe("w'd's'd");
  });

  it('Does not send while isComposing is still set', async () => {
    const { send, textarea } = await composer();

    const event = new KeyboardEvent('keydown', {
      key: 'Enter',
      isComposing: true,
      bubbles: true,
      cancelable: true
    });
    textarea.element.dispatchEvent(event);
    await flushPromises();

    expect(event.defaultPrevented).toBe(false);
    expect(send).not.toHaveBeenCalled();
  });

  it('Keeps the composed text until the user sends it with a second Enter', async () => {
    const { send, textarea } = await composer();
    const element = textarea.element;
    const now = vi.spyOn(Date, 'now').mockReturnValue(1_000);

    element.dispatchEvent(new CompositionEvent('compositionstart', { bubbles: true, data: '' }));
    element.value = '我的时代';
    element.dispatchEvent(new CompositionEvent('compositionupdate', { bubbles: true, data: '我的时代' }));
    element.dispatchEvent(
      new InputEvent('input', {
        bubbles: true,
        data: '我的时代',
        inputType: 'insertCompositionText',
        isComposing: true
      })
    );
    press(element, {}, 229);
    await flushPromises();

    expect(send).not.toHaveBeenCalled();
    expect(element.value).toBe('我的时代');

    element.dispatchEvent(new CompositionEvent('compositionend', { bubbles: true, data: '我的时代' }));
    element.dispatchEvent(
      new InputEvent('input', { bubbles: true, data: '我的时代', inputType: 'insertCompositionText' })
    );
    now.mockReturnValue(1_010);
    const confirmingEnter = press(element);
    await flushPromises();

    expect(confirmingEnter.defaultPrevented).toBe(false);
    expect(send).not.toHaveBeenCalled();

    now.mockReturnValue(1_100);
    const sendingEnter = press(element);
    await flushPromises();

    expect(sendingEnter.defaultPrevented).toBe(true);
    expect(send).toHaveBeenCalledWith('我的时代', []);
  });

  it('Sends a plain Enter', async () => {
    const { send, textarea } = await composer();

    const event = press(textarea.element);
    await flushPromises();

    expect(event.defaultPrevented).toBe(true);
    expect(send).toHaveBeenCalledTimes(1);
    expect(send).toHaveBeenCalledWith("w'd's'd", []);
  });
});
