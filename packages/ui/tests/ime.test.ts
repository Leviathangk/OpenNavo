import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { isImeComposing, isImeEnter, resetImeComposition, runOnEnter } from '../src/utils/ime';

function enter(init: KeyboardEventInit = {}, keyCode = 13) {
  const event = new KeyboardEvent('keydown', { key: 'Enter', bubbles: true, cancelable: true, ...init });
  Object.defineProperty(event, 'keyCode', { value: keyCode });
  return event;
}

function composition(kind: 'compositionstart' | 'compositionend') {
  document.dispatchEvent(new Event(kind, { bubbles: true }));
}

beforeEach(() => resetImeComposition());
afterEach(() => {
  resetImeComposition();
  vi.restoreAllMocks();
});

describe('IME-safe Enter', () => {
  it('Runs a plain Enter and prevents its default action', () => {
    const action = vi.fn();
    const event = enter();
    runOnEnter(event, action);
    expect(action).toHaveBeenCalledOnce();
    expect(event.defaultPrevented).toBe(true);
  });

  it('Ignores Enter while the candidate window is open', () => {
    const action = vi.fn();
    expect(isImeEnter(enter({ isComposing: true }))).toBe(true);
    expect(isImeEnter(enter({}, 229))).toBe(true);
    runOnEnter(enter({ isComposing: true }), action);
    expect(action).not.toHaveBeenCalled();
  });

  it('Ignores the confirming Enter WebKit sends just after compositionend', () => {
    const now = vi.spyOn(Date, 'now').mockReturnValue(1_000);
    composition('compositionstart');
    composition('compositionend');

    now.mockReturnValue(1_030);
    expect(isImeComposing()).toBe(true);
    expect(isImeEnter(enter())).toBe(true);

    now.mockReturnValue(1_100);
    expect(isImeComposing()).toBe(false);
    expect(isImeEnter(enter())).toBe(false);
  });

  it('Cancels the implicit form submission of a confirming Enter', () => {
    const form = document.createElement('form');
    const input = document.createElement('input');
    form.append(input);
    document.body.append(form);
    const submit = vi.fn((event: Event) => event.preventDefault());
    form.addEventListener('submit', submit);

    const now = vi.spyOn(Date, 'now').mockReturnValue(2_000);
    composition('compositionstart');
    composition('compositionend');
    now.mockReturnValue(2_010);
    const confirming = enter();
    input.dispatchEvent(confirming);
    expect(confirming.defaultPrevented).toBe(true);
    expect(submit).not.toHaveBeenCalled();

    now.mockReturnValue(2_100);
    const deliberate = enter();
    input.dispatchEvent(deliberate);
    expect(deliberate.defaultPrevented).toBe(false);
    form.remove();
  });
});
