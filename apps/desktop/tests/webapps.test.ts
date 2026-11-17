import { DOMWrapper, flushPromises, mount } from '@vue/test-utils';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { i18n } from '@/i18n';
import { WEBAPP_CATALOG } from '@/components/webapps/catalog';
import WebAppsPage from '@/pages/WebAppsPage.vue';
import { setup } from './helpers';

afterEach(() => {
  document.body.innerHTML = '';
  history.replaceState(null, '', '/');
});

async function mountPage() {
  const { pinia, router } = await setup('/webapps');
  const wrapper = mount(WebAppsPage, {
    global: { plugins: [pinia, router, i18n] },
    attachTo: document.body
  });
  await flushPromises();
  return wrapper;
}

function dialogButton(text: string) {
  const element = [...document.querySelectorAll<HTMLButtonElement>('[role="alertdialog"] button')].find(
    button => button.textContent?.trim() === text
  );
  if (!element) throw new Error(`missing dialog button: ${text}`);
  return new DOMWrapper(element);
}

function bodyButton(text: string) {
  const element = [...document.body.querySelectorAll<HTMLButtonElement>('button')].find(
    button => button.textContent?.trim() === text
  );
  if (!element) throw new Error(`missing button: ${text}`);
  return new DOMWrapper(element);
}

describe('Web Apps', () => {
  it('adds a built-in app and restores its card after deletion', async () => {
    const wrapper = await mountPage();
    expect(wrapper.text()).toContain('内置应用');
    expect(wrapper.text()).toContain('ChatGPT');

    const catalogCard = wrapper.find('[data-catalog-id="chatgpt"]');
    expect(catalogCard.text()).toContain('添加');
    await catalogCard.find('button').trigger('click');
    await flushPromises();

    expect(catalogCard.text()).toContain('已添加');

    await wrapper
      .findAll('button')
      .find(button => button.text().includes('我的应用'))!
      .trigger('click');
    await flushPromises();

    expect(wrapper.text()).toContain('ChatGPT');
    expect(wrapper.text()).toContain('chatgpt.com');

    await wrapper
      .findAll('button')
      .find(button => button.text() === '删除')!
      .trigger('click');
    await flushPromises();
    await dialogButton('删除').trigger('click');
    await flushPromises();

    expect(wrapper.find('[data-catalog-id="chatgpt"]').text()).toContain('添加');
  });

  it('shows every built-in app with its real icon', async () => {
    const wrapper = await mountPage();

    const cards = wrapper.findAll('[data-catalog-id]');
    expect(cards).toHaveLength(WEBAPP_CATALOG.length);
    expect(cards.every(card => card.find('img').exists())).toBe(true);
    expect(wrapper.find('[data-catalog-id="threads"]').text()).toContain('Threads');
  });

  it('creates a custom app and deletes it', async () => {
    const wrapper = await mountPage();

    await wrapper
      .findAll('button')
      .find(button => button.text() === '新建网页应用')!
      .trigger('click');
    await flushPromises();

    await new DOMWrapper(document.querySelector('#webapp-name')!).setValue('Docs');
    await new DOMWrapper(document.querySelector('input[type="url"]')!).setValue('docs.example.com');
    await bodyButton('创建').trigger('click');
    await flushPromises();

    await wrapper
      .findAll('button')
      .find(button => button.text().includes('我的应用'))!
      .trigger('click');
    await flushPromises();

    expect(wrapper.text()).toContain('Docs');
    expect(wrapper.text()).toContain('docs.example.com');

    await wrapper
      .findAll('button')
      .find(button => button.text() === '删除')!
      .trigger('click');
    await flushPromises();
    await dialogButton('删除').trigger('click');
    await flushPromises();

    expect(wrapper.text()).toContain('内置应用');
  });

  it('Does not create an app when Enter confirms an IME candidate', async () => {
    await mountPage();

    await bodyButton('新建网页应用').trigger('click');
    await flushPromises();
    await new DOMWrapper(document.querySelector('#webapp-name')!).setValue('Docs');
    const url = new DOMWrapper(document.querySelector('input[type="url"]')!);
    await url.setValue('docs.example.com');

    await url.trigger('keydown', { key: 'Enter', isComposing: true });
    await flushPromises();
    expect(document.querySelector('#webapp-name')).not.toBeNull();

    await url.trigger('keydown', { key: 'Enter' });
    await flushPromises();
    expect(document.querySelector('#webapp-name')).toBeNull();
  });

  it('Does not create an app when Enter arrives just after compositionend', async () => {
    await mountPage();

    await bodyButton('新建网页应用').trigger('click');
    await flushPromises();
    await new DOMWrapper(document.querySelector('#webapp-name')!).setValue('Docs');
    const url = new DOMWrapper(document.querySelector('input[type="url"]')!);
    await url.setValue('docs.example.com');

    const now = vi.spyOn(Date, 'now').mockReturnValue(1_000);
    try {
      url.element.dispatchEvent(new CompositionEvent('compositionend', { bubbles: true }));
      now.mockReturnValue(1_025);
      url.element.dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', bubbles: true }));
      await flushPromises();
      expect(document.querySelector('#webapp-name')).not.toBeNull();

      now.mockReturnValue(1_051);
      url.element.dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', bubbles: true }));
      await flushPromises();
      expect(document.querySelector('#webapp-name')).toBeNull();
    } finally {
      now.mockRestore();
    }
  });
});
