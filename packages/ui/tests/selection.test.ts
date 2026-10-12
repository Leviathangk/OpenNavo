import { mount } from '@vue/test-utils';
import { describe, expect, it } from 'vitest';
import OnCheckbox from '../src/components/OnCheckbox.vue';
import OnTable from '../src/components/OnTable.vue';
import { withLocale } from './helpers';

interface Row {
  id: string;
  name: string;
  locked?: boolean;
}

const rows: Row[] = [
  { id: 'a', name: 'Alpha' },
  { id: 'b', name: 'Beta' },
  { id: 'c', name: 'Self', locked: true }
];

function table(selected: string[] = [], extra: Record<string, unknown> = {}) {
  return mount(OnTable<Row>, {
    props: {
      columns: [{ key: 'name', label: '名称' }],
      rows,
      rowKey: (row: Row) => row.id,
      selectable: true,
      selected,
      canSelect: (row: Row) => !row.locked,
      selectAllLabel: '全选',
      selectRowLabel: (row: Row) => `选择 ${row.name}`,
      ...extra
    },
    global: withLocale('zh-CN')
  });
}

describe('OnCheckbox', () => {
  it('Emits the new value and exposes the mixed state only as a DOM property', async () => {
    const wrapper = mount(OnCheckbox, { props: { modelValue: false, indeterminate: true, ariaLabel: '选择' } });
    const input = wrapper.get('input');
    expect((input.element as HTMLInputElement).indeterminate).toBe(true);
    expect(input.attributes('aria-checked')).toBe('mixed');
    await input.setValue(true);
    expect(wrapper.emitted('update:modelValue')?.at(-1)).toEqual([true]);
    await wrapper.setProps({ modelValue: true });
    expect((input.element as HTMLInputElement).indeterminate).toBe(false);
  });

  it('Locked boxes stay checked and cannot change', () => {
    const wrapper = mount(OnCheckbox, { props: { modelValue: false, locked: true, ariaLabel: 'App' } });
    const input = wrapper.get('input');
    expect((input.element as HTMLInputElement).checked).toBe(true);
    expect(input.attributes('disabled')).toBeDefined();
  });
});

describe('OnTable selection', () => {
  it('Header box selects every selectable row and skips protected rows', async () => {
    const wrapper = table();
    await wrapper.get('input[aria-label="全选"]').setValue(true);
    expect(wrapper.emitted('update:selected')?.at(-1)).toEqual([['a', 'b']]);
    expect(wrapper.get('input[aria-label="选择 Self"]').attributes('disabled')).toBeDefined();
  });

  it('Header box is mixed for a partial selection and clears only visible rows', async () => {
    // "z" belongs to a row hidden by the host's filter and must survive select-all toggles.
    const wrapper = table(['a', 'b', 'z']);
    const header = wrapper.get('input[aria-label="全选"]');
    expect((header.element as HTMLInputElement).checked).toBe(true);
    await header.setValue(false);
    expect(wrapper.emitted('update:selected')?.at(-1)).toEqual([['z']]);

    const partial = table(['a']);
    expect((partial.get('input[aria-label="全选"]').element as HTMLInputElement).indeterminate).toBe(true);
  });

  it('Row boxes toggle a single key without triggering the row click', async () => {
    const wrapper = table(['a']);
    await wrapper.get('input[aria-label="选择 Beta"]').setValue(true);
    expect(wrapper.emitted('update:selected')?.at(-1)).toEqual([['a', 'b']]);
    await wrapper.get('input[aria-label="选择 Alpha"]').trigger('click');
    expect(wrapper.emitted('rowClick')).toBeUndefined();
    expect(wrapper.findAll('tbody tr')[0]?.attributes('aria-selected')).toBe('true');
  });

  it('Renders no checkbox column unless selectable', () => {
    const wrapper = table([], { selectable: false });
    expect(wrapper.find('input[type="checkbox"]').exists()).toBe(false);
    expect(wrapper.findAll('thead th')).toHaveLength(1);
  });
});
