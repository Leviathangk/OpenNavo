import { afterEach, describe, expect, it, vi } from 'vitest';
import { nextTick, ref, watch } from 'vue';
import { easeOutCubic } from '../app/utils/landing';
import { useCountUp, useDemoLoop, useLandingParallax, useRevealOnScroll } from '../app/composables/useLandingMotion';

function setup(reduced = false, initial = 0) {
  let mount = () => {};
  let unmount = () => {};
  let intersect = (_entries: { isIntersecting: boolean }[]) => {};
  const changes = new EventTarget();
  const preference = Object.assign(changes, { matches: reduced });
  const disconnect = vi.fn();
  class Observer {
    constructor(callback: typeof intersect) {
      intersect = callback;
    }
    observe = vi.fn();
    disconnect = disconnect;
  }
  vi.useFakeTimers();
  vi.stubGlobal('ref', ref);
  vi.stubGlobal('onMounted', (callback: () => void) => {
    mount = callback;
  });
  vi.stubGlobal('onBeforeUnmount', (callback: () => void) => {
    unmount = callback;
  });
  vi.stubGlobal('window', { IntersectionObserver: Observer, matchMedia: () => preference });
  vi.stubGlobal('IntersectionObserver', Observer);
  const step = useDemoLoop(ref({} as HTMLElement), [100, 100, 100], initial);
  mount();
  return {
    step,
    unmount,
    disconnect,
    visible: (value: boolean) => intersect([{ isIntersecting: value }]),
    reduce: (value: boolean) => {
      preference.matches = value;
      changes.dispatchEvent(new Event('change'));
    }
  };
}

afterEach(() => {
  vi.useRealTimers();
  vi.unstubAllGlobals();
});

describe('Demos react live to reduced motion', () => {
  it('Enabling clears timers/resets representative frame; disabling resumes; unmount cleans listeners', () => {
    const demo = setup(false, 1);
    demo.visible(true);
    vi.advanceTimersByTime(100);
    expect(demo.step.value).toBe(2);
    demo.reduce(true);
    expect(demo.step.value).toBe(1);
    expect(vi.getTimerCount()).toBe(0);
    vi.advanceTimersByTime(500);
    expect(demo.step.value).toBe(1);
    demo.reduce(false);
    vi.advanceTimersByTime(100);
    expect(demo.step.value).toBe(2);
    demo.unmount();
    expect(demo.disconnect).toHaveBeenCalledOnce();
    demo.reduce(true);
    expect(demo.step.value).toBe(2);
    demo.reduce(false);
    expect(vi.getTimerCount()).toBe(0);
  });

  it('Initial reduced motion still observes changes; offscreen loops remain stopped', () => {
    const demo = setup(true);
    demo.visible(true);
    expect(vi.getTimerCount()).toBe(0);
    demo.visible(false);
    demo.reduce(false);
    expect(vi.getTimerCount()).toBe(0);
    demo.visible(true);
    vi.advanceTimersByTime(100);
    expect(demo.step.value).toBe(1);
    demo.visible(false);
    expect(vi.getTimerCount()).toBe(0);
    demo.unmount();
  });
});

function animationEnvironment(reduced = false) {
  let mount = () => {};
  let unmount = () => {};
  let intersect = (_entries: { isIntersecting: boolean }[]) => {};
  const preference = Object.assign(new EventTarget(), { matches: reduced });
  const fine = Object.assign(new EventTarget(), { matches: true });
  const win = Object.assign(new EventTarget(), {
    innerWidth: 1000,
    innerHeight: 800,
    matchMedia: (query: string) => (query.includes('pointer') ? fine : preference),
    IntersectionObserver: true
  });
  const disconnect = vi.fn();
  vi.stubGlobal('ref', ref);
  vi.stubGlobal('watch', watch);
  vi.stubGlobal('easeOutCubic', easeOutCubic);
  vi.stubGlobal('onMounted', (callback: () => void) => {
    mount = callback;
  });
  vi.stubGlobal('onBeforeUnmount', (callback: () => void) => {
    unmount = callback;
  });
  vi.stubGlobal('window', win);
  vi.stubGlobal(
    'IntersectionObserver',
    class {
      constructor(callback: typeof intersect) {
        intersect = callback;
      }
      observe = vi.fn();
      disconnect = disconnect;
    }
  );
  let frameId = 0;
  const frames = new Map<number, FrameRequestCallback>();
  vi.stubGlobal('requestAnimationFrame', (callback: FrameRequestCallback) => {
    frames.set(++frameId, callback);
    return frameId;
  });
  vi.stubGlobal('cancelAnimationFrame', (id: number) => frames.delete(id));
  const style = new Map<string, string>();
  const target = ref({
    getBoundingClientRect: () => ({ top: 1000 }),
    style: {
      setProperty: (key: string, value: string) => style.set(key, value),
      removeProperty: (key: string) => style.delete(key)
    }
  } as unknown as HTMLElement);
  return {
    target,
    style,
    frames,
    disconnect,
    mount: () => mount(),
    unmount: () => unmount(),
    visible: () => intersect([{ isIntersecting: true }]),
    reduce: (value: boolean) => {
      preference.matches = value;
      preference.dispatchEvent(new Event('change'));
    },
    move: () => win.dispatchEvent(Object.assign(new Event('pointermove'), { clientX: 800, clientY: 600 })),
    tick: (now = performance.now() + 100) => {
      const queued = [...frames.values()];
      frames.clear();
      queued.forEach(callback => callback(now));
    }
  };
}

describe('Hero parallax and catalog counts react to reduced motion', () => {
  it('Reset parallax/cancel frames, ignore pointer frames while reduced, restore/unmount correctly', () => {
    const env = animationEnvironment();
    useLandingParallax(env.target);
    env.mount();
    env.move();
    env.tick();
    expect(env.style.get('--landing-px')).toBe('0.300');
    env.move();
    expect(env.frames.size).toBe(1);
    env.reduce(true);
    expect(env.frames.size).toBe(0);
    expect(env.style.size).toBe(0);
    env.move();
    expect(env.frames.size).toBe(0);
    env.reduce(false);
    env.move();
    env.tick();
    expect(env.style.get('--landing-py')).toBe('0.250');
    env.unmount();
    env.reduce(true);
    env.reduce(false);
    env.move();
    expect(env.frames.size).toBe(0);
    expect(env.style.size).toBe(0);
  });

  it.each([false, true])(
    'Reduced motion completes counts before entry or during playback; started=%s',
    async started => {
      const env = animationEnvironment();
      const value = ref(1000);
      const current = useCountUp(env.target, () => value.value);
      env.mount();
      expect(current.value).toBe(0);
      if (started) {
        env.visible();
        env.tick();
        expect(current.value).toBeGreaterThan(0);
        expect(current.value).toBeLessThan(1000);
      }
      env.reduce(true);
      expect(current.value).toBe(1000);
      expect(env.frames.size).toBe(0);
      expect(env.disconnect).toHaveBeenCalled();
      env.visible();
      env.tick();
      expect(current.value).toBe(1000);
      env.reduce(false);
      env.visible();
      expect(env.frames.size).toBe(0);
      value.value = 1200;
      await nextTick();
      expect(current.value).toBe(1200);
      env.unmount();
      expect(env.frames.size).toBe(0);
    }
  );

  it('Initial reduced motion shows final counts and allows parallax after disabling', () => {
    const env = animationEnvironment(true);
    const current = useCountUp(env.target, () => 1000);
    env.mount();
    expect(current.value).toBe(1000);
    env.visible();
    expect(env.frames.size).toBe(0);
    env.unmount();
    useLandingParallax(env.target);
    env.mount();
    env.move();
    expect(env.frames.size).toBe(0);
    env.reduce(false);
    env.move();
    expect(env.frames.size).toBe(1);
    env.unmount();
    expect(env.frames.size).toBe(0);
  });
});

describe('Reveal layout and first paint', () => {
  it.each([false, true])('Leaves first-screen content visible and batches offscreen reads; reduced=%s', reduced => {
    let mount = () => {};
    let unmount = () => {};
    let notify = (_entries: { isIntersecting: boolean; target: HTMLElement }[]) => {};
    const operations: string[] = [];
    const targets = [100, 800, 1200].map((top, index) => {
      let reveal = '';
      return {
        getBoundingClientRect: () => {
          operations.push(`read:${index}`);
          return { top };
        },
        dataset: {
          get reveal() {
            return reveal;
          },
          set reveal(value: string) {
            operations.push(`write:${index}`);
            reveal = value;
          }
        }
      } as unknown as HTMLElement;
    });
    const observe = vi.fn();
    const unobserve = vi.fn();
    const disconnect = vi.fn();
    vi.stubGlobal('window', { innerHeight: 800, IntersectionObserver: true, matchMedia: () => ({ matches: reduced }) });
    vi.stubGlobal('onMounted', (callback: () => void) => {
      mount = callback;
    });
    vi.stubGlobal('onBeforeUnmount', (callback: () => void) => {
      unmount = callback;
    });
    vi.stubGlobal(
      'IntersectionObserver',
      class {
        constructor(callback: typeof notify) {
          notify = callback;
        }
        observe = observe;
        unobserve = unobserve;
        disconnect = disconnect;
      }
    );
    useRevealOnScroll(ref({ querySelectorAll: () => targets } as unknown as HTMLElement));
    // SSR/setup never hides content or measures layout.
    expect(operations).toEqual([]);
    mount();
    expect(targets[0]?.dataset.reveal).toBe('');
    if (reduced) {
      expect(operations).toEqual([]);
      expect(observe).not.toHaveBeenCalled();
    } else {
      expect(operations).toEqual(['read:0', 'read:1', 'read:2', 'write:1', 'write:2']);
      expect(observe).toHaveBeenCalledTimes(2);
      const target = targets[1]!;
      notify([{ isIntersecting: false, target }]);
      expect(target.dataset.reveal).toBe('pending');
      notify([{ isIntersecting: true, target }]);
      expect(target.dataset.reveal).toBe('shown');
      expect(unobserve).toHaveBeenCalledWith(target);
    }
    unmount();
    if (!reduced) expect(disconnect).toHaveBeenCalledOnce();
  });
});
