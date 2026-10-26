import type { Ref } from 'vue';

// Landing motion (08 §10.14): SSR renders a complete static scene, visible without scripts or with reduced motion (08 §5).
// Add animation only after mounting and confirming motion is allowed.

type Target = Ref<HTMLElement | null | undefined>;

function motionAllowed(): boolean {
  return (
    typeof window !== 'undefined' &&
    'IntersectionObserver' in window &&
    !window.matchMedia('(prefers-reduced-motion: reduce)').matches
  );
}

/**
 * Reveal data-reveal descendants once as they enter the viewport; leave initially visible elements unchanged to avoid flashing them away.
 * Only below-viewport elements become pending; animate on entry with each element's --landing-delay.
 */
export function useRevealOnScroll(root: Target) {
  let observer: IntersectionObserver | undefined;

  onMounted(() => {
    const element = root.value;
    if (!element || !motionAllowed()) return;
    const reveal = new IntersectionObserver(
      entries => {
        for (const entry of entries) {
          if (!entry.isIntersecting) continue;
          (entry.target as HTMLElement).dataset.reveal = 'shown';
          reveal.unobserve(entry.target);
        }
      },
      { rootMargin: '0px 0px -8% 0px' }
    );
    observer = reveal;
    const fold = window.innerHeight;
    // Read every position before changing styles: interleaving them forces a layout per element.
    const pending = [...element.querySelectorAll<HTMLElement>('[data-reveal]')].filter(
      target => target.getBoundingClientRect().top >= fold
    );
    for (const target of pending) {
      target.dataset.reveal = 'pending';
      reveal.observe(target);
    }
  });

  onBeforeUnmount(() => observer?.disconnect());
}

/**
 * Demo step loop: durations[i] is step i's dwell time in milliseconds; play only within the viewport, pause outside.
 * When motion is disabled, remain at initial, each demo's representative frame.
 */
export function useDemoLoop(target: Target, durations: readonly number[], initial = 0) {
  const step = ref(initial);
  let timer: ReturnType<typeof setTimeout> | undefined;
  let observer: IntersectionObserver | undefined;
  let visible = false;
  let preference: MediaQueryList | undefined;

  function updateMotion() {
    clearTimeout(timer);
    if (preference?.matches) step.value = initial;
    else schedule();
  }

  function schedule() {
    clearTimeout(timer);
    if (!visible || preference?.matches || !durations.length) return;
    timer = setTimeout(() => {
      step.value = (step.value + 1) % durations.length;
      schedule();
    }, durations[step.value] ?? 1000);
  }

  onMounted(() => {
    const element = target.value;
    if (!element || !('IntersectionObserver' in window)) return;
    preference = window.matchMedia('(prefers-reduced-motion: reduce)');
    preference.addEventListener('change', updateMotion);
    observer = new IntersectionObserver(
      ([entry]) => {
        visible = Boolean(entry?.isIntersecting);
        if (visible) schedule();
        else clearTimeout(timer);
      },
      { threshold: 0.2 }
    );
    observer.observe(element);
  });

  onBeforeUnmount(() => {
    clearTimeout(timer);
    observer?.disconnect();
    preference?.removeEventListener('change', updateMotion);
  });

  return step;
}

/** Pointer parallax requires a fine pointer and permitted motion; reset on preference changes to avoid residual offsets. */
export function useLandingParallax(target: Target) {
  let preference: MediaQueryList | undefined;
  let finePointer: MediaQueryList | undefined;
  let frame = 0;
  let pointer: { x: number; y: number } | null = null;

  function stop() {
    window.removeEventListener('pointermove', onPointerMove);
    cancelAnimationFrame(frame);
    frame = 0;
    pointer = null;
    target.value?.style.removeProperty('--landing-px');
    target.value?.style.removeProperty('--landing-py');
  }

  function apply() {
    frame = 0;
    if (!pointer || !target.value || preference?.matches || !finePointer?.matches) return;
    target.value.style.setProperty('--landing-px', pointer.x.toFixed(3));
    target.value.style.setProperty('--landing-py', pointer.y.toFixed(3));
  }

  function onPointerMove(event: PointerEvent) {
    if (preference?.matches || !finePointer?.matches) return;
    pointer = { x: event.clientX / window.innerWidth - 0.5, y: event.clientY / window.innerHeight - 0.5 };
    if (!frame) frame = requestAnimationFrame(apply);
  }

  function updateMotion() {
    stop();
    if (!preference?.matches && finePointer?.matches) {
      window.addEventListener('pointermove', onPointerMove, { passive: true });
    }
  }

  onMounted(() => {
    preference = window.matchMedia('(prefers-reduced-motion: reduce)');
    finePointer = window.matchMedia('(pointer: fine)');
    preference.addEventListener('change', updateMotion);
    finePointer.addEventListener('change', updateMotion);
    updateMotion();
  });

  onBeforeUnmount(() => {
    stop();
    preference?.removeEventListener('change', updateMotion);
    finePointer?.removeEventListener('change', updateMotion);
  });
}

/**
 * Count-up: SSR/initial render shows final values so no-script and screen-reader users get accurate numbers.
 * Below-viewport counts reset after mounting, then animate to final values over duration milliseconds on entry.
 */
export function useCountUp(target: Target, value: () => number, duration = 1400) {
  const current = ref(value());
  let observer: IntersectionObserver | undefined;
  let frame = 0;
  let running = false;
  let settled = false;
  let preference: MediaQueryList | undefined;

  function finish() {
    settled = true;
    observer?.disconnect();
    cancelAnimationFrame(frame);
    frame = 0;
    running = false;
    current.value = value();
  }

  function updateMotion() {
    if (preference?.matches) finish();
  }

  watch(value, next => {
    if (!running) current.value = next;
  });

  onMounted(() => {
    const element = target.value;
    if (!element || !('IntersectionObserver' in window)) return;
    preference = window.matchMedia('(prefers-reduced-motion: reduce)');
    preference.addEventListener('change', updateMotion);
    if (preference.matches || element.getBoundingClientRect().top < window.innerHeight) {
      finish();
      return;
    }
    current.value = 0;
    observer = new IntersectionObserver(
      ([entry]) => {
        if (settled || preference?.matches || !entry?.isIntersecting) return;
        observer?.disconnect();
        running = true;
        const start = performance.now();
        const tick = (now: number) => {
          if (settled || preference?.matches) return;
          const progress = Math.min(1, (now - start) / duration);
          current.value = Math.round(value() * easeOutCubic(progress));
          if (progress < 1) frame = requestAnimationFrame(tick);
          else finish();
        };
        frame = requestAnimationFrame(tick);
      },
      { threshold: 0.5 }
    );
    observer.observe(element);
  });

  onBeforeUnmount(() => {
    finish();
    preference?.removeEventListener('change', updateMotion);
  });

  return current;
}
