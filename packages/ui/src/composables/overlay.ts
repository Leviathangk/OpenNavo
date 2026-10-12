// Shared overlay behavior (dialogs, sheets, palette, lightbox, menus): scroll lock, focus trapping, and script focus (08 §8.22, §13).

const FOCUSABLE = [
  'a[href]',
  'button:not([disabled])',
  'input:not([disabled])',
  'select:not([disabled])',
  'textarea:not([disabled])',
  '[tabindex]:not([tabindex="-1"])'
].join(', ');

// WebKit rings script focus unless the user clicked in this document first, so overlays the app opens itself (menu bar
// popover, deep links, startup prompts, routes) would ring their first control. Ring script focus only during keyboard
// navigation; shortcuts such as Command-K and bare modifier presses do not count.
const MODIFIERS = new Set(['Alt', 'CapsLock', 'Control', 'Meta', 'Shift']);
let keyboard = false;

if (typeof document !== 'undefined') {
  document.addEventListener(
    'keydown',
    event => {
      if (!event.metaKey && !event.ctrlKey && !event.altKey && !MODIFIERS.has(event.key)) keyboard = true;
    },
    true
  );
  document.addEventListener(
    'pointerdown',
    () => {
      keyboard = false;
    },
    true
  );
  // Returning from another window (the menu bar popover, another app) starts without keyboard navigation.
  window.addEventListener('blur', () => {
    keyboard = false;
  });
}

/** Whether the latest interaction in this window was keyboard navigation rather than a pointer. */
export function usingKeyboard(): boolean {
  return keyboard;
}

// Text fields show focus however it arrives, as native fields do.
const TEXT_ENTRY =
  'textarea, input:not([type="button"]):not([type="checkbox"]):not([type="radio"]):not([type="reset"]):not([type="submit"])';

/** Focus from script; only keyboard navigation (or a text field) shows the focus ring. */
export function moveFocus(element: HTMLElement | null | undefined): void {
  if (!element) return;
  if (keyboard || element.matches(TEXT_ENTRY)) element.focus();
  else element.focus({ focusVisible: false });
}

// Reference-count nested overlays; restore scrolling only after the final one closes.
let locks = 0;
let savedOverflow = '';

export function lockScroll(): void {
  if (locks === 0) {
    savedOverflow = document.documentElement.style.overflow;
    document.documentElement.style.overflow = 'hidden';
  }
  locks += 1;
}

export function unlockScroll(): void {
  if (locks === 0) return;
  locks -= 1;
  if (locks === 0) document.documentElement.style.overflow = savedOverflow;
}

/** Tab-focusable descendants in document order. */
export function focusableIn(container: HTMLElement): HTMLElement[] {
  return Array.from(container.querySelectorAll<HTMLElement>(FOCUSABLE)).filter(
    element => element.getAttribute('aria-hidden') !== 'true'
  );
}

/** Cycle Tab inside the container; stay put if none are focusable. */
export function trapTab(event: KeyboardEvent, container: HTMLElement): void {
  const items = focusableIn(container);
  const first = items[0];
  const last = items.at(-1);
  if (!first || !last) {
    event.preventDefault();
    return;
  }
  const active = document.activeElement;
  const outside = !(active instanceof Node) || !container.contains(active);
  if (event.shiftKey && (active === first || outside)) {
    event.preventDefault();
    last.focus();
  } else if (!event.shiftKey && (active === last || outside)) {
    event.preventDefault();
    first.focus();
  }
}
