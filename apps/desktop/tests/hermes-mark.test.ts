import { mount } from '@vue/test-utils';
import { describe, expect, it } from 'vitest';
import { LOGO } from '@opennavo/shared';
import HermesMark from '@/components/hermes/HermesMark.vue';

describe('Hermes mark', () => {
  it('uses the fuller small Polaris cut at 32 px and below', () => {
    const small = mount(HermesMark, { props: { size: 30 } });
    const large = mount(HermesMark, { props: { size: 54 } });

    expect(small.get('path').attributes('d')).toBe(LOGO.small.innerPath);
    expect(large.get('path').attributes('d')).toBe(LOGO.innerPath);
  });

  it('draws the mark with the brand gradients', () => {
    const stops = mount(HermesMark, { props: { size: 30 } })
      .findAll('stop')
      .map(stop => stop.attributes('stop-color'));

    expect(stops).toEqual([
      ...LOGO.outerGradient.map(stop => stop.color),
      ...LOGO.innerGradientOnLight.map(stop => stop.color)
    ]);
  });
});
