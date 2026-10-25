import { LOCALE_METADATA } from '@opennavo/shared';

type WebPageInput = NonNullable<Parameters<typeof defineWebPage>[0]>;

export interface PageSchemaInput {
  /** WebPage subtype: CollectionPage for lists, ProfilePage for authors, AboutPage, and so on. */
  type?: string;
  /** @id of the entity the page is (an app overview, an article, a list). */
  mainEntity?: string;
  /**
   * @id of the entity a subpage is about (an app's versions page is about the app). Only a reference: the
   * schema.org resolver reads an `about` that carries any other property as an Organization of its own.
   */
  about?: string;
  /** @id of the page's main image node. */
  imageId?: string;
  dateModified?: string | null;
}

/**
 * The page's WebPage node (T4). Every page has exactly one, with its canonical URL as @id, the page
 * language, isPartOf the one WebSite and its breadcrumb; pages add what they are about by calling this
 * again, and nodes with the same @id merge. The WebSite and Organization live in app.vue.
 */
export function usePageSchema(input: MaybeRefOrGetter<PageSchemaInput> = {}) {
  const canonical = useCanonicalUrl();
  const { locale } = useI18n();
  const siteUrl = useRuntimeConfig().public.siteUrl.replace(/\/$/, '');
  useSchemaOrg(
    computed(() => {
      const { type, mainEntity, about, imageId, dateModified } = toValue(input);
      const url = canonical.value;
      const node: Record<string, unknown> = {
        '@id': `${url}#webpage`,
        url,
        inLanguage: LOCALE_METADATA[toAppLocale(locale.value)].hreflang,
        isPartOf: { '@id': `${siteUrl}/#website` },
        ...(type ? { '@type': type } : {}),
        ...(mainEntity ? { mainEntity: { '@id': mainEntity } } : {}),
        ...(about ? { about: { '@id': about } } : {}),
        ...(imageId ? { primaryImageOfPage: { '@id': imageId } } : {}),
        ...(dateModified ? { dateModified } : {})
      };
      return [defineWebPage(node as WebPageInput)];
    })
  );
}
