<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue';
import { useRoute } from 'vue-router';
import type { UploadCustomRequestOptions } from 'naive-ui';
import type { ArticleImageInfo } from '@opennavo/ui/markdown';
import {
  fetchArticle,
  fetchArticleAuthors,
  publishArticle,
  restoreArticle,
  revertArticle,
  setArticleLocale,
  setArticleSlug,
  submitArticle,
  updateArticle,
  uploadAsset,
  withdrawArticle
} from '@/service/api';
import type { DataOf, RecordOf, Schemas } from '@/typings/api/opennavo';
import { useAuth } from '@/hooks/business/auth';
import { useLocalizedPackage } from '@/hooks/business/localized';
import { formatDateTime, isDryRun } from '@/utils/opennavo';
import { CONTENT_LOCALE_OPTIONS, localeName } from '@/utils/content-locale';
import { $t } from '@/locales';
import ArticlePreview from './modules/article-preview.vue';
import ClaimEvidence from './modules/claim-evidence.vue';
import {
  ARTICLE_SLUG,
  ARTICLE_TYPES,
  MAX_PACKAGES,
  MAX_SOURCES,
  changedFields,
  formFromView,
  formProblems,
  packagesChanged,
  publicationProblems
} from './modules/article';
import type { Article, ArticleForm } from './modules/article';

defineOptions({ name: 'ContentArticleDetail' });

// Article editor (WP16): the working revision on the left with the site's own preview, metadata,
// review actions, languages of the published revision, revisions and the review record on the right.
// Edits never touch the live page; publishing needs content:article:publish.
const route = useRoute();
const { hasAuth } = useAuth();
const { nameOf } = useLocalizedPackage();
const canPublish = computed(() => hasAuth('content:article:publish'));
const id = computed(() => Number(route.params.id));
const detail = ref<Article | null>(null);
const form = ref<ArticleForm | null>(null);
const authors = ref<DataOf<'listArticleAuthors'>>([]);
const extraImages = ref<ArticleImageInfo[]>([]);
const loading = ref(false);
const busy = ref('');
const view = ref<'edit' | 'split' | 'preview'>('split');
const urlPlaceholder = 'https://';

const text = (key: string, values?: Record<string, unknown>) =>
  values
    ? $t(`page.content.articleDetail.${key}` as App.I18n.I18nKey, values)
    : $t(`page.content.articleDetail.${key}` as App.I18n.I18nKey);
const listText = (key: string) => $t(`page.content.article.${key}` as App.I18n.I18nKey);

function apply(value: Article) {
  detail.value = value;
  form.value = formFromView(value);
  extraImages.value = [];
}

async function load() {
  if (!Number.isFinite(id.value)) return;
  loading.value = true;
  const [{ data, error }, list] = await Promise.all([fetchArticle(id.value), fetchArticleAuthors()]);
  loading.value = false;
  if (!list.error) authors.value = list.data;
  if (!error) apply(data);
}

watch(id, load, { immediate: true });

const typeOptions = computed(() => ARTICLE_TYPES.map(value => ({ value, label: listText(`types.${value}`) })));
const authorOptions = computed(() => [
  { value: 0, label: listText('editorial') },
  ...authors.value.map(author => ({ value: author.id, label: author.name }))
]);
const authorValue = computed({
  get: () => form.value?.authorId ?? 0,
  set: value => {
    if (form.value) form.value.authorId = value || null;
  }
});
const images = computed<ArticleImageInfo[]>(() => [...(detail.value?.images ?? []), ...extraImages.value]);
const dirty = computed(() =>
  Boolean(
    detail.value &&
    form.value &&
    (Object.keys(changedFields(form.value, detail.value)).length > 0 || packagesChanged(form.value, detail.value))
  )
);
const localProblems = computed(() => (form.value ? formProblems(form.value) : []));
const publicationIssues = computed(() => (form.value ? publicationProblems(form.value) : []));
const problems = computed(() => [
  ...new Set([...(detail.value?.readiness ?? []), ...localProblems.value, ...publicationIssues.value])
]);
const working = computed(() => detail.value?.working);
const submitted = computed(() => Boolean(working.value?.submittedAt) && !working.value?.publishedAt);
const isPublishedRevision = computed(() => Boolean(working.value?.publishedAt));
const STATUS_TAG: Record<Schemas['ArticleStatus'], 'default' | 'warning' | 'success' | 'error'> = {
  draft: 'default',
  review: 'warning',
  published: 'success',
  archived: 'error'
};

async function run(name: string, call: () => ReturnType<typeof fetchArticle>, success?: string) {
  if (busy.value) return false;
  busy.value = name;
  try {
    const { data, error } = await call();
    if (error || !data || isDryRun(data)) return false;
    apply(data);
    if (success) window.$message?.success(success);
    return true;
  } finally {
    busy.value = '';
  }
}

async function save() {
  const article = detail.value;
  const current = form.value;
  if (!article || !current || !dirty.value) return true;
  if (localProblems.value.length) {
    window.$message?.warning(text('fixFirst'));
    return false;
  }
  const patch = changedFields(current, article);
  if (packagesChanged(current, article)) patch.packageIds = current.packages.map(item => item.id);
  const ok = await run('save', () => updateArticle(article.id, { expectedRevision: article.revision, ...patch }));
  if (!ok) return false;
  window.$message?.success($t('page.shared.saved'));
  return true;
}

async function submit() {
  if (publicationIssues.value.length || !(await save()) || !detail.value) return;
  const article = detail.value;
  await run('submit', () => submitArticle(article.id, article.revision), text('submitted'));
}

function confirm(title: string, content: string, action: () => Promise<unknown>) {
  window.$dialog?.warning({
    title,
    content,
    positiveText: $t('common.confirm'),
    negativeText: $t('common.cancel'),
    onPositiveClick: async () => {
      await action();
    }
  });
}

function publish() {
  const article = detail.value;
  if (!article) return;
  confirm(text('publish'), text('publishConfirm'), () =>
    run('publish', () => publishArticle(article.id, article.revision), text('publishedNow'))
  );
}

const withdrawReason = ref('');
function withdraw() {
  const article = detail.value;
  if (!article) return;
  confirm(text('withdraw'), text('withdrawConfirm'), () =>
    run(
      'withdraw',
      () => withdrawArticle(article.id, article.revision, withdrawReason.value.trim() || null),
      text('withdrawn')
    )
  );
}

function restore() {
  const article = detail.value;
  if (!article) return;
  run('restore', () => restoreArticle(article.id, article.revision), text('restored'));
}

function revert(number: number) {
  const article = detail.value;
  if (!article) return;
  confirm(text('revert'), text('revertConfirm', { number }), () =>
    run('revert', () => revertArticle(article.id, article.revision, number), text('reverted', { number }))
  );
}

const slugEditing = ref(false);
const slugDraft = ref('');
async function saveSlug() {
  const article = detail.value;
  if (!article || !ARTICLE_SLUG.test(slugDraft.value.trim())) {
    window.$message?.warning(listText('slugInvalid'));
    return;
  }
  const ok = await run(
    'slug',
    () => setArticleSlug(article.id, article.revision, slugDraft.value.trim()),
    $t('page.shared.saved')
  );
  if (ok) slugEditing.value = false;
}

function toggleLocale(locale: Schemas['Locale'], withheld: boolean) {
  const article = detail.value;
  if (!article) return;
  run('locale', () => setArticleLocale(article.id, locale, article.revision, withheld));
}

function addSource() {
  if (form.value && form.value.sources.length < MAX_SOURCES) form.value.sources.push({ title: '', url: '' });
}

function addPackage(row: RecordOf<'listAdminPackages'>) {
  const current = form.value;
  if (!current || current.packages.length >= MAX_PACKAGES || current.packages.some(item => item.id === row.id)) return;
  current.packages.push({ id: row.id, token: row.token, displayName: nameOf(row), iconUrl: row.iconUrl ?? null });
}

async function uploadCover({ file, onFinish, onError }: UploadCustomRequestOptions) {
  if (!file.file || !form.value) return onError();
  const { data, error } = await uploadAsset(file.file, 'cover');
  if (error || isDryRun(data)) return onError();
  form.value.coverAssetId = data.id;
  form.value.coverUrl = data.url;
  onFinish();
}

// Body images need alt text before they go in, so the insert dialog asks for it first.
const imageDialog = reactive({ show: false, alt: '' });
async function uploadBodyImage({ file, onFinish, onError }: UploadCustomRequestOptions) {
  const alt = imageDialog.alt.trim();
  if (!file.file || !form.value || !alt) return onError();
  const { data, error } = await uploadAsset(file.file, 'screenshot');
  if (error || isDryRun(data)) return onError();
  extraImages.value.push({ url: data.url, width: data.width ?? null, height: data.height ?? null });
  form.value.bodyMarkdown = `${form.value.bodyMarkdown.trimEnd()}\n\n![${alt.replace(/[[\]]/g, '')}](${data.url})\n`;
  Object.assign(imageDialog, { show: false, alt: '' });
  onFinish();
}

// Capture and report registration need saved package membership, but incomplete
// claims must remain editable until those operations return their real IDs.
async function preparePublicationReferences() {
  if (!detail.value || !form.value || busy.value) return false;
  const current = form.value;
  const article = detail.value;
  if (!packagesChanged(current, article)) return true;
  const packages = current.packages.map(item => item.id);
  // An old package target cannot survive unlinking its app. Keep the local target
  // untouched so the next save can atomically persist the editor's replacement.
  const target = article.working.target;
  const clearTarget = target?.entityKey.startsWith('pkg:') && !packages.includes(Number(target.entityKey.slice(4)));
  busy.value = 'references';
  try {
    const { data, error } = await updateArticle(article.id, {
      expectedRevision: article.revision,
      packageIds: packages,
      ...(clearTarget ? { target: null } : {})
    });
    if (error || !data || isDryRun(data)) return false;
    // Preserve the form and child instances while updating the revision token.
    detail.value = data;
    return true;
  } finally {
    busy.value = '';
  }
}
const targetIntents: Schemas['ArticleSeoTarget']['intent'][] = [
  'download',
  'install',
  'brew',
  'uninstall',
  'alternative',
  'price',
  'version',
  'requirements',
  'compare',
  'topic'
];
const claimKinds = computed(() => ['factual', 'test'].map(value => ({ value, label: text(value) })));
const claimPackages = computed(
  () => form.value?.packages.map(item => ({ value: item.id, label: item.displayName })) ?? []
);
function addClaim() {
  form.value?.claims.push({
    text: '',
    kind: 'factual',
    evidenceId: 0,
    excerpt: '',
    appVersion: '',
    region: 'ZZ',
    packageId: null,
    testArtifactId: null
  });
}
const serpTitle = computed(() => form.value?.seoTitle.trim() || form.value?.title.trim() || '—');
const serpDescription = computed(() => form.value?.seoDescription.trim() || form.value?.summary.trim() || '');
const articlePath = computed(() => `/articles/${detail.value?.slug ?? ''}`);
</script>

<template>
  <NSpin :show="loading && !detail">
    <div v-if="detail && form && working" class="flex-col gap-16px">
      <NCard :bordered="false" size="small" class="card-wrapper">
        <div class="flex flex-wrap items-start gap-12px">
          <div class="min-w-0 flex-1 flex-col gap-4px">
            <div class="flex flex-wrap items-center gap-8px">
              <span class="text-18px font-600">{{ working.title || detail.slug }}</span>
              <NTag size="small" :bordered="false" :type="STATUS_TAG[detail.status]">
                {{ listText(`statuses.${detail.status}`) }}
              </NTag>
              <NTag v-if="detail.published && detail.hasPendingChanges" size="small" :bordered="false" type="info">
                {{ submitted ? listText('pendingReview') : listText('pendingDraft') }}
              </NTag>
            </div>
            <div class="flex flex-wrap items-center gap-8px text-12px">
              <template v-if="!slugEditing">
                <span class="font-mono">{{ articlePath }}</span>
                <NButton v-if="canPublish" text size="tiny" @click="((slugDraft = detail.slug), (slugEditing = true))">
                  {{ text('changeSlug') }}
                </NButton>
              </template>
              <template v-else>
                <NInput v-model:value="slugDraft" size="tiny" class="w-260px font-mono" :maxlength="100" />
                <NButton size="tiny" type="primary" :loading="busy === 'slug'" @click="saveSlug">
                  {{ text('save') }}
                </NButton>
                <NButton size="tiny" @click="slugEditing = false">{{ $t('common.cancel') }}</NButton>
              </template>
              <NText depth="3">{{ text('revisionInfo', { number: working.number, revision: detail.revision }) }}</NText>
              <a v-if="detail.webUrl" :href="detail.webUrl" target="_blank" rel="noopener noreferrer">
                {{ text('viewOnSite') }}
              </a>
            </div>
          </div>
          <div class="flex flex-wrap items-center gap-8px">
            <NButton :disabled="!dirty" :loading="busy === 'save'" @click="save">{{ text('save') }}</NButton>
            <NButton
              v-if="!isPublishedRevision && !submitted"
              type="primary"
              ghost
              :disabled="problems.length > 0 && !dirty"
              :loading="busy === 'submit'"
              @click="submit"
            >
              {{ text('submit') }}
            </NButton>
            <NButton
              v-if="canPublish && submitted"
              type="primary"
              :disabled="dirty || publicationIssues.length > 0"
              :loading="busy === 'publish'"
              @click="publish"
            >
              {{ text('publish') }}
            </NButton>
            <NButton
              v-if="canPublish && detail.status === 'published'"
              type="error"
              ghost
              :loading="busy === 'withdraw'"
              @click="withdraw"
            >
              {{ text('withdraw') }}
            </NButton>
            <NButton v-if="canPublish && detail.status === 'archived'" :loading="busy === 'restore'" @click="restore">
              {{ text('restore') }}
            </NButton>
          </div>
        </div>
        <NAlert v-if="problems.length" type="warning" class="mt-12px" data-testid="article-problems">
          <div class="mb-4px font-500">{{ text('problemsTitle') }}</div>
          <ul class="m-0 pl-18px">
            <li v-for="problem in problems" :key="problem">{{ text(`problems.${problem}`) }}</li>
          </ul>
        </NAlert>
        <NText v-if="submitted" depth="3" class="mt-8px block text-12px">
          {{ text('submittedBy', { name: working.submittedByName || '—', time: formatDateTime(working.submittedAt) }) }}
        </NText>
        <NText v-if="isPublishedRevision" depth="3" class="mt-8px block text-12px">{{ text('editForks') }}</NText>
      </NCard>

      <div class="grid grid-cols-1 items-start gap-16px xl:grid-cols-[minmax(0,1fr)_380px]">
        <NCard :bordered="false" size="small" class="card-wrapper" :title="text('content')">
          <NForm label-placement="top">
            <div class="grid grid-cols-1 gap-x-16px md:grid-cols-3">
              <NFormItem :label="$t('page.shared.i18n.sourceLocale')">
                <NSelect v-model:value="form.sourceLocale" :options="CONTENT_LOCALE_OPTIONS" />
              </NFormItem>
              <NFormItem :label="listText('columns.type')">
                <NSelect v-model:value="form.type" :options="typeOptions" />
              </NFormItem>
              <NFormItem :label="text('author')">
                <NSelect v-model:value="authorValue" :options="authorOptions" />
              </NFormItem>
            </div>
            <NFormItem :label="listText('fields.title')">
              <NInput
                v-model:value="form.title"
                :maxlength="200"
                show-count
                :input-props="{ 'aria-label': listText('fields.title') }"
              />
            </NFormItem>
            <NFormItem :label="text('summary')">
              <NInput
                v-model:value="form.summary"
                type="textarea"
                :maxlength="400"
                show-count
                :autosize="{ minRows: 2, maxRows: 4 }"
                :placeholder="text('summaryHint')"
                :input-props="{ 'aria-label': text('summary') }"
              />
            </NFormItem>
            <NFormItem :label="text('body')">
              <div class="w-full flex-col gap-8px">
                <div class="flex flex-wrap items-center gap-8px">
                  <NRadioGroup v-model:value="view" size="small">
                    <NRadioButton value="edit">{{ $t('page.shared.markdown.edit') }}</NRadioButton>
                    <NRadioButton value="split">{{ text('split') }}</NRadioButton>
                    <NRadioButton value="preview">{{ $t('page.shared.markdown.preview') }}</NRadioButton>
                  </NRadioGroup>
                  <NButton size="small" @click="imageDialog.show = true">{{ text('insertImage') }}</NButton>
                  <NText depth="3" class="text-12px">{{ text('bodyHint') }}</NText>
                </div>
                <div class="grid gap-12px" :class="view === 'split' ? 'lg:grid-cols-2' : 'grid-cols-1'">
                  <NInput
                    v-if="view !== 'preview'"
                    v-model:value="form.bodyMarkdown"
                    type="textarea"
                    :maxlength="100000"
                    :autosize="{ minRows: 18, maxRows: 40 }"
                    class="font-mono"
                    :input-props="{ 'aria-label': text('body') }"
                  />
                  <ArticlePreview
                    v-if="view !== 'edit'"
                    :source="form.bodyMarkdown"
                    :images="images"
                    :title="form.title"
                  />
                </div>
              </div>
            </NFormItem>
            <NDivider>{{ text('publication') }}</NDivider>
            <NText depth="3" class="mb-12px block">{{ text('publicationHint') }}</NText>
            <NButton
              v-if="!form.target"
              size="small"
              @click="form.target = { entityKey: `topic:${detail.slug}`, intent: 'topic' }"
            >
              {{ text('addTarget') }}
            </NButton>
            <div v-else class="grid grid-cols-1 gap-x-16px md:grid-cols-2">
              <NFormItem :label="text('targetEntity')">
                <NInput
                  v-model:value="form.target.entityKey"
                  :maxlength="300"
                  :input-props="{ 'aria-label': text('targetEntity') }"
                />
              </NFormItem>
              <NFormItem :label="text('targetIntent')">
                <NSelect
                  v-model:value="form.target.intent"
                  :options="targetIntents.map(value => ({ value, label: value }))"
                />
              </NFormItem>
            </div>
            <fieldset v-for="(claim, index) in form.claims" :key="index" class="mb-16px min-w-0 border-0 p-0">
              <legend class="mb-8px">{{ text('claimNumber', { number: index + 1 }) }}</legend>
              <NFormItem :label="text('claimText')">
                <NInput
                  v-model:value="claim.text"
                  type="textarea"
                  :maxlength="20000"
                  :input-props="{ 'aria-label': text('claimText') }"
                />
              </NFormItem>
              <div class="grid grid-cols-1 gap-x-16px md:grid-cols-2">
                <NFormItem :label="text('claimKind')">
                  <NSelect v-model:value="claim.kind" :options="claimKinds" />
                </NFormItem>
                <NFormItem :label="text('claimPackage')">
                  <NSelect v-model:value="claim.packageId" clearable :options="claimPackages" />
                </NFormItem>
                <NFormItem :label="text('evidenceId')">
                  <NInputNumber v-model:value="claim.evidenceId" :min="1" :precision="0" />
                </NFormItem>
                <NFormItem :label="text('appVersion')">
                  <NInput v-model:value="claim.appVersion" :maxlength="100" />
                </NFormItem>
                <NFormItem :label="text('region')"><NInput v-model:value="claim.region" :maxlength="2" /></NFormItem>
                <NFormItem :label="text('testArtifactId')">
                  <NInputNumber v-model:value="claim.testArtifactId" :min="1" :precision="0" clearable />
                </NFormItem>
              </div>
              <NFormItem :label="text('excerpt')">
                <NInput
                  v-model:value="claim.excerpt"
                  type="textarea"
                  :maxlength="20000"
                  :input-props="{ 'aria-label': text('excerpt') }"
                />
              </NFormItem>
              <ClaimEvidence
                :article-id="detail.id"
                :claim="claim"
                :can-publish="canPublish"
                :prepare="preparePublicationReferences"
                @change="value => Object.assign(claim, value)"
              />
              <NButton size="small" type="error" quaternary @click="form.claims.splice(index, 1)">
                {{ $t('common.delete') }}
              </NButton>
            </fieldset>
            <NButton class="mb-16px" :disabled="form.claims.length >= 200" @click="addClaim">
              {{ text('addClaim') }}
            </NButton>
            <NFormItem :label="text('testNote')">
              <NInput
                v-model:value="form.testNote"
                type="textarea"
                :maxlength="1000"
                show-count
                :autosize="{ minRows: 2, maxRows: 4 }"
                :placeholder="text('testNoteHint')"
                :input-props="{ 'aria-label': text('testNote') }"
              />
            </NFormItem>
          </NForm>
        </NCard>

        <div class="flex-col gap-16px">
          <NCard :bordered="false" size="small" class="card-wrapper" :title="text('cover')">
            <div class="flex-col gap-8px">
              <div class="flex flex-wrap items-center gap-8px">
                <NImage v-if="form.coverUrl" :src="form.coverUrl" width="160" height="90" object-fit="cover" />
                <NUpload
                  accept="image/png,image/jpeg,image/webp"
                  :show-file-list="false"
                  :custom-request="uploadCover"
                  class="w-auto"
                >
                  <NButton size="small">{{ text('uploadCover') }}</NButton>
                </NUpload>
                <NButton
                  v-if="form.coverAssetId"
                  size="small"
                  quaternary
                  type="error"
                  @click="((form.coverAssetId = null), (form.coverUrl = null), (form.coverAlt = ''))"
                >
                  {{ $t('common.delete') }}
                </NButton>
              </div>
              <NInput
                v-if="form.coverAssetId"
                v-model:value="form.coverAlt"
                :maxlength="300"
                :placeholder="text('coverAlt')"
                :input-props="{ 'aria-label': text('coverAlt') }"
              />
            </div>
          </NCard>

          <NCard :bordered="false" size="small" class="card-wrapper" :title="text('packages')">
            <NText depth="3" class="mb-8px block text-12px">{{ text('packagesHint') }}</NText>
            <div v-for="(item, index) in form.packages" :key="item.id" class="mb-6px flex items-center gap-8px">
              <NAvatar v-if="item.iconUrl" :src="item.iconUrl" :size="20" color="transparent" />
              <span class="min-w-0 flex-1 truncate">{{ item.displayName }}</span>
              <NText depth="3" class="font-mono text-12px">{{ item.token }}</NText>
              <NButton size="tiny" quaternary type="error" @click="form.packages.splice(index, 1)">
                {{ $t('common.delete') }}
              </NButton>
            </div>
            <PackagePicker
              :disabled="form.packages.length >= MAX_PACKAGES"
              :placeholder="text('addPackage')"
              @pick="addPackage"
            />
          </NCard>

          <NCard :bordered="false" size="small" class="card-wrapper" :title="text('sources')">
            <NText depth="3" class="mb-8px block text-12px">{{ text('sourcesHint') }}</NText>
            <div v-for="(source, index) in form.sources" :key="index" class="mb-8px flex-col gap-4px">
              <div class="flex gap-6px">
                <NInput v-model:value="source.title" size="small" :maxlength="200" :placeholder="text('sourceTitle')" />
                <NButton size="small" quaternary type="error" @click="form.sources.splice(index, 1)">
                  {{ $t('common.delete') }}
                </NButton>
              </div>
              <NInput
                v-model:value="source.url"
                size="small"
                :maxlength="2048"
                :placeholder="urlPlaceholder"
                class="font-mono"
              />
            </div>
            <NButton size="small" :disabled="form.sources.length >= MAX_SOURCES" @click="addSource">
              {{ text('addSource') }}
            </NButton>
          </NCard>

          <NCard :bordered="false" size="small" class="card-wrapper" :title="text('search')">
            <NForm label-placement="top" size="small">
              <NFormItem :label="text('seoTitle')">
                <NInput v-model:value="form.seoTitle" :maxlength="120" show-count :placeholder="text('seoTitleHint')" />
              </NFormItem>
              <NFormItem :label="text('seoDescription')">
                <NInput
                  v-model:value="form.seoDescription"
                  type="textarea"
                  :maxlength="300"
                  show-count
                  :autosize="{ minRows: 2, maxRows: 4 }"
                  :placeholder="text('seoDescriptionHint')"
                />
              </NFormItem>
            </NForm>
            <div class="rounded-default bg-gray-100 px-12px py-8px dark:bg-dark-300" data-testid="serp-preview">
              <div class="truncate text-15px text-blue-600">{{ serpTitle }}</div>
              <div class="truncate text-12px text-green-700">{{ articlePath }}</div>
              <div class="line-clamp-2 text-12px">{{ serpDescription }}</div>
            </div>
            <NText depth="3" class="mt-4px block text-12px">{{ text('serpHint') }}</NText>
          </NCard>

          <NCard
            v-if="detail.locales.length"
            :bordered="false"
            size="small"
            class="card-wrapper"
            :title="text('languages')"
          >
            <div v-for="item in detail.locales" :key="item.locale" class="mb-6px flex items-center gap-8px text-13px">
              <span class="w-96px">{{ localeName(item.locale) }}</span>
              <NTag
                size="small"
                :bordered="false"
                :type="item.status === 'failed' ? 'error' : item.status === 'pending' ? 'warning' : 'default'"
              >
                {{ text(`localeStatus.${item.source ? 'source' : item.status}`) }}
              </NTag>
              <NText v-if="item.failReason" depth="3" class="text-12px">{{ item.failReason }}</NText>
              <NSwitch
                v-if="canPublish && !item.source && item.status !== 'missing'"
                class="ml-auto"
                size="small"
                :value="!item.withheld"
                :loading="busy === 'locale'"
                @update:value="value => toggleLocale(item.locale, !value)"
              >
                <template #checked>{{ text('shown') }}</template>
                <template #unchecked>{{ text('held') }}</template>
              </NSwitch>
            </div>
            <NText depth="3" class="block text-12px">{{ text('languagesHint') }}</NText>
          </NCard>

          <NCard :bordered="false" size="small" class="card-wrapper" :title="text('revisions')">
            <div v-for="item in detail.revisions" :key="item.number" class="mb-6px flex items-center gap-8px text-13px">
              <span class="w-36px font-mono">#{{ item.number }}</span>
              <div class="min-w-0 flex-1 flex-col">
                <span class="truncate">{{ item.title }}</span>
                <NText depth="3" class="text-12px">
                  {{ formatDateTime(item.createdAt) }} · {{ item.createdByName || '—' }}
                  <template v-if="item.publishedAt">· {{ text('wasPublished') }}</template>
                </NText>
              </div>
              <NButton
                v-if="item.number !== working.number"
                size="tiny"
                quaternary
                :loading="busy === 'revert'"
                @click="revert(item.number)"
              >
                {{ text('revert') }}
              </NButton>
            </div>
          </NCard>

          <NCard :bordered="false" size="small" class="card-wrapper" :title="text('events')">
            <div v-for="(event, index) in detail.events" :key="index" class="mb-4px text-12px">
              <span class="font-500">{{ text(`actions.${event.action}`) }}</span>
              <template v-if="event.revisionNumber">#{{ event.revisionNumber }}</template>
              <NText depth="3">
                · {{ event.actorName || text(`actorTypes.${event.actorType}`) }} · {{ formatDateTime(event.createdAt) }}
              </NText>
            </div>
            <NInput
              v-if="canPublish && detail.status === 'published'"
              v-model:value="withdrawReason"
              size="small"
              class="mt-8px"
              :maxlength="500"
              :placeholder="text('withdrawReason')"
            />
          </NCard>
        </div>
      </div>
    </div>
    <NModal v-model:show="imageDialog.show" preset="card" :title="text('insertImage')" class="max-w-480px">
      <NForm label-placement="top">
        <NFormItem :label="text('imageAlt')">
          <NInput v-model:value="imageDialog.alt" :maxlength="300" :placeholder="text('imageAltHint')" />
        </NFormItem>
        <NUpload
          accept="image/png,image/jpeg,image/webp"
          :show-file-list="false"
          :disabled="!imageDialog.alt.trim()"
          :custom-request="uploadBodyImage"
        >
          <NButton type="primary" :disabled="!imageDialog.alt.trim()">{{ text('chooseImage') }}</NButton>
        </NUpload>
      </NForm>
    </NModal>
  </NSpin>
</template>
