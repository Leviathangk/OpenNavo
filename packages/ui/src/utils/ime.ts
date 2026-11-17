// Enter belongs to the IME while a candidate list is open. WebKit can also dispatch the confirming
// Enter just after compositionend with isComposing already false, so keep a short grace window.
const GRACE_MS = 50;

let composing = false;
let compositionEndedAt = Number.NEGATIVE_INFINITY;
let tracked = false;

function track() {
  if (tracked || typeof document === 'undefined') return;
  tracked = true;
  document.addEventListener(
    'compositionstart',
    () => {
      composing = true;
    },
    true
  );
  document.addEventListener(
    'compositionend',
    () => {
      composing = false;
      compositionEndedAt = Date.now();
    },
    true
  );
  // A confirming Enter can still submit the surrounding form after compositionend. Cancel only its
  // default action; the input's own keydown handler decides whether the app should act on it.
  document.addEventListener(
    'keydown',
    event => {
      if (event.key !== 'Enter' || event.isComposing || !isTextEntry(event.target) || !isImeComposing()) return;
      event.preventDefault();
    },
    true
  );
}

function isTextEntry(target: EventTarget | null): boolean {
  if (!(target instanceof HTMLElement)) return false;
  return target.isContentEditable || target.tagName === 'INPUT' || target.tagName === 'TEXTAREA';
}

// Start before the first keydown so a Safari compositionend that precedes the confirming Enter is seen.
track();

/** Whether Enter is part of an active or just-finished IME composition. */
export function isImeEnter(event: KeyboardEvent): boolean {
  track();
  return event.isComposing || event.keyCode === 229 || composing || Date.now() - compositionEndedAt < GRACE_MS;
}

/** Whether an IME is composing now or finished within the Enter grace window. */
export function isImeComposing(): boolean {
  track();
  return composing || Date.now() - compositionEndedAt < GRACE_MS;
}

/** Run an Enter action only when the key is not selecting an IME candidate. */
export function runOnEnter(event: KeyboardEvent, action: () => void): void {
  if (isImeEnter(event)) return;
  event.preventDefault();
  action();
}

/** Clear composition state between simulated IME sessions in tests. */
export function resetImeComposition(): void {
  composing = false;
  compositionEndedAt = Number.NEGATIVE_INFINITY;
}
