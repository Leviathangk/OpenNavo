import { nextTick } from 'vue';
import { describe, expect, it } from 'vitest';
import {
  queueHermesNotificationOpen,
  takeHermesNotificationOpen,
  watchHermesNotificationOpen
} from '@/hermes/notificationOpen';

describe('Hermes notification open queue', () => {
  it('keeps a request queued until the assistant page consumes it', () => {
    queueHermesNotificationOpen({ conversationId: 'chat-1' });

    expect(takeHermesNotificationOpen()).toEqual({ conversationId: 'chat-1' });
    expect(takeHermesNotificationOpen()).toBeNull();
  });

  it('delivers a request to a mounted assistant page', async () => {
    const seen: string[] = [];
    const stop = watchHermesNotificationOpen(open => seen.push(open.conversationId));

    queueHermesNotificationOpen({ conversationId: 'chat-2' });
    await nextTick();

    expect(seen).toEqual(['chat-2']);
    expect(takeHermesNotificationOpen()).toBeNull();
    stop();
  });
});
