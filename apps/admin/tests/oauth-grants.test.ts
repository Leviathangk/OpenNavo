import { flushPromises, mount } from '@vue/test-utils';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { defineComponent, h } from 'vue';
import OAuthGrants from '../src/views/user-center/modules/oauth-grants.vue';

const api = vi.hoisted(() => vi.fn());
vi.mock('@/service/api/oauth', () => ({ oauthRequest: api }));
vi.mock('vue-i18n', () => ({ useI18n: () => ({ t: (key: string) => key }) }));
vi.mock('@/views/system/agent/modules/permissions', () => ({ permissionLabel: (code: string) => code }));
vi.mock('@/utils/opennavo', () => ({ formatDateTime: (value: string) => value }));

const container = defineComponent({
  setup:
    (_, { slots }) =>
    () =>
      h('div', slots.default?.())
});
const button = defineComponent({
  props: { disabled: Boolean },
  setup:
    (props, { slots }) =>
    () =>
      h('button', { disabled: props.disabled }, slots.default?.())
});
const confirmation = defineComponent({
  emits: ['positive-click'],
  setup:
    (_, { slots, emit }) =>
    () =>
      h('div', [slots.trigger?.(), h('button', { onClick: () => emit('positive-click') }, 'confirm revoke')])
});
const stubs = {
  NCard: container,
  NAlert: container,
  NList: container,
  NListItem: container,
  NTag: container,
  NSpin: container,
  NEmpty: container,
  NButton: button,
  NPopconfirm: confirmation
};
function grant(id: number, revoked = false) {
  return {
    id,
    name: `Application ${id}`,
    scopes: 'catalog:package:view',
    allowDelete: false,
    expiresAt: '2027-01-01T00:00:00Z',
    revokedAt: revoked ? '2026-01-01T00:00:00Z' : null
  };
}
function loadMore(wrapper: ReturnType<typeof mount>) {
  return wrapper.findAll('button').find(item => item.text() === 'loadMore')!;
}
beforeEach(() => api.mockReset());

describe('OAuth grant pagination', () => {
  it('loads an older active grant behind 100 revoked ones and revokes it without losing the loaded pages', async () => {
    api
      .mockResolvedValueOnce({ items: Array.from({ length: 100 }, (_, n) => grant(201 - n, true)), nextCursor: '102' })
      .mockResolvedValueOnce({ items: [grant(101)], nextCursor: null })
      .mockResolvedValueOnce({});
    const wrapper = mount(OAuthGrants, { global: { stubs } });
    await flushPromises();
    expect(wrapper.text()).not.toContain('Application 101');
    await loadMore(wrapper).trigger('click');
    await flushPromises();
    expect(api).toHaveBeenNthCalledWith(2, 'grants?before=102');
    expect(wrapper.text()).toContain('Application 201');
    expect(wrapper.text()).toContain('Application 101');
    expect(loadMore(wrapper)).toBeUndefined();
    await wrapper
      .findAll('button')
      .find(item => item.text() === 'confirm revoke')!
      .trigger('click');
    await flushPromises();
    expect(api).toHaveBeenLastCalledWith('grants/101', { method: 'DELETE' });
    expect(api).toHaveBeenCalledTimes(3);
    expect(wrapper.text()).toContain('Application 101');
    expect(wrapper.findAll('button').some(item => item.text() === 'confirm revoke')).toBe(false);
    wrapper.unmount();
  });
  it('keeps the page and cursor after a failed request so loading more can be retried', async () => {
    api
      .mockResolvedValueOnce({ items: [grant(102, true)], nextCursor: '102' })
      .mockRejectedValueOnce(new Error('offline'))
      .mockResolvedValueOnce({ items: [grant(101)], nextCursor: null });
    const wrapper = mount(OAuthGrants, { global: { stubs } });
    await flushPromises();
    await loadMore(wrapper).trigger('click');
    await flushPromises();
    expect(wrapper.text()).toContain('Application 102');
    await loadMore(wrapper).trigger('click');
    await flushPromises();
    expect(api).toHaveBeenNthCalledWith(3, 'grants?before=102');
    expect(wrapper.text()).toContain('Application 101');
    wrapper.unmount();
  });
});
