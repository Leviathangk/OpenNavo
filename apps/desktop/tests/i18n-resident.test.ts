import { describe, expect, it } from 'vitest';
import en from '../src/i18n/en-US';

describe('resident monitor notice', () => {
  it('uses the final English wording from issue #130', () => {
    const resident = en.monitor.settings.resident;
    expect(resident.noticeTitle).toBe('{monitorName} is now in the menu bar');
    expect(resident.noticeBody).toBe(
      'It keeps running after you quit OpenNavo, so your menu bar readings stay live. Click an item to see it without opening the app. On by default — turn it off in Settings → {systemMonitor}, or in System Settings → General → {loginItemsPane}.'
    );
    expect(resident.noticeAction).toBe('Open Monitor Settings');
    expect(resident.noticeDismiss).toBe('Got it');
  });
});
