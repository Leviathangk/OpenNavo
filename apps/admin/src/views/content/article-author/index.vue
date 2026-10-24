<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue';
import type { UploadCustomRequestOptions } from 'naive-ui';
import { createArticleAuthor, fetchArticleAuthors, updateArticleAuthor, uploadAsset } from '@/service/api';
import type { DataOf, Schemas } from '@/typings/api/opennavo';
import { formatDateTime, isDryRun } from '@/utils/opennavo';
import { SOURCE_LOCALE_OPTIONS, DEFAULT_SOURCE_LOCALE } from '@/utils/content-locale';
import { $t } from '@/locales';
import { ARTICLE_SLUG } from '../article-detail/modules/article';

defineOptions({ name: 'ContentArticleAuthor' });

// Authors (WP14/WP16): public profiles for bylines and author pages. Role and bio are translated
// like other content; changes appear on the site at once, so this needs content:article:publish.
type Author = DataOf<'listArticleAuthors'>[number];

const authors = ref<Author[]>([]);
const loading = ref(false);
const saving = ref(false);
const editing = ref<number | null | undefined>(undefined);
const form = reactive({
  slug: '',
  name: '',
  sourceLocale: DEFAULT_SOURCE_LOCALE as Schemas['Locale'],
  role: '',
  bio: '',
  links: [] as Schemas['ArticleSource'][],
  avatarAssetId: null as number | null,
  avatarUrl: null as string | null
});
const text = (key: string) => $t(`page.content.articleAuthor.${key}` as App.I18n.I18nKey);
const urlPlaceholder = 'https://';
const valid = computed(
  () =>
    ARTICLE_SLUG.test(form.slug.trim()) &&
    form.name.trim().length > 0 &&
    form.links.every(link => link.title.trim() && /^https:\/\/\S+$/.test(link.url.trim()))
);

async function load() {
  loading.value = true;
  const { data, error } = await fetchArticleAuthors();
  loading.value = false;
  if (!error) authors.value = data;
}

function open(author?: Author) {
  editing.value = author?.id ?? null;
  Object.assign(form, {
    slug: author?.slug ?? '',
    name: author?.name ?? '',
    sourceLocale: author?.sourceLocale ?? DEFAULT_SOURCE_LOCALE,
    role: author?.role ?? '',
    bio: author?.bio ?? '',
    links: author?.links.map(link => ({ ...link })) ?? [],
    avatarAssetId: author?.avatarAssetId ?? null,
    avatarUrl: author?.avatarUrl ?? null
  });
}

async function uploadAvatar({ file, onFinish, onError }: UploadCustomRequestOptions) {
  if (!file.file) return onError();
  const { data, error } = await uploadAsset(file.file, 'cover');
  if (error || isDryRun(data)) return onError();
  form.avatarAssetId = data.id;
  form.avatarUrl = data.url;
  onFinish();
}

async function save() {
  if (!valid.value || saving.value) return;
  saving.value = true;
  const body = {
    slug: form.slug.trim(),
    name: form.name.trim(),
    sourceLocale: form.sourceLocale,
    role: form.role.trim() || null,
    bio: form.bio.trim() || null,
    links: form.links.map(link => ({ title: link.title.trim(), url: link.url.trim() })),
    avatarAssetId: form.avatarAssetId
  };
  const { error } = editing.value ? await updateArticleAuthor(editing.value, body) : await createArticleAuthor(body);
  saving.value = false;
  if (error) return;
  window.$message?.success($t('page.shared.saved'));
  editing.value = undefined;
  await load();
}

onMounted(load);
</script>

<template>
  <div class="flex-col gap-16px">
    <NCard :title="text('title')" :bordered="false" size="small" class="card-wrapper">
      <template #header-extra>
        <NButton size="small" type="primary" @click="open()">{{ text('create') }}</NButton>
      </template>
      <NText depth="3" class="mb-12px block text-12px">{{ text('hint') }}</NText>
      <NSpin :show="loading">
        <NEmpty v-if="!authors.length" :description="text('empty')" />
        <NList v-else bordered>
          <NListItem v-for="author in authors" :key="author.id">
            <div class="flex items-center gap-12px">
              <NAvatar v-if="author.avatarUrl" :src="author.avatarUrl" round :size="32" />
              <div class="min-w-0 flex-1 flex-col">
                <span class="font-600">{{ author.name }}</span>
                <NText depth="3" class="text-12px">
                  {{ `/authors/${author.slug}` }}
                  <template v-if="author.role">· {{ author.role }}</template>
                </NText>
              </div>
              <NText depth="3" class="text-12px">{{ text('articles') }} {{ author.articleCount }}</NText>
              <NText depth="3" class="text-12px">{{ formatDateTime(author.updatedAt) }}</NText>
              <NButton size="small" ghost type="primary" @click="open(author)">{{ $t('common.edit') }}</NButton>
            </div>
          </NListItem>
        </NList>
      </NSpin>
    </NCard>
    <NModal
      :show="editing !== undefined"
      preset="card"
      :title="editing ? text('edit') : text('create')"
      class="max-w-560px"
      @close="editing = undefined"
    >
      <NForm label-placement="top">
        <div class="grid grid-cols-2 gap-x-16px">
          <NFormItem :label="text('name')">
            <NInput v-model:value="form.name" :maxlength="80" />
          </NFormItem>
          <NFormItem :label="text('slug')">
            <NInput v-model:value="form.slug" :maxlength="80" class="font-mono" />
          </NFormItem>
        </div>
        <div class="grid grid-cols-2 gap-x-16px">
          <NFormItem :label="text('role')">
            <NInput v-model:value="form.role" :maxlength="80" />
          </NFormItem>
          <NFormItem :label="$t('page.shared.i18n.sourceLocale')">
            <NSelect v-model:value="form.sourceLocale" :options="SOURCE_LOCALE_OPTIONS" />
          </NFormItem>
        </div>
        <NFormItem :label="text('bio')">
          <NInput
            v-model:value="form.bio"
            type="textarea"
            :maxlength="1000"
            show-count
            :autosize="{ minRows: 3, maxRows: 6 }"
          />
        </NFormItem>
        <NFormItem :label="text('avatar')">
          <div class="flex items-center gap-8px">
            <NAvatar v-if="form.avatarUrl" :src="form.avatarUrl" round :size="40" />
            <NUpload accept="image/png,image/jpeg,image/webp" :show-file-list="false" :custom-request="uploadAvatar">
              <NButton size="small">{{ text('upload') }}</NButton>
            </NUpload>
            <NButton
              v-if="form.avatarAssetId"
              size="small"
              quaternary
              type="error"
              @click="((form.avatarAssetId = null), (form.avatarUrl = null))"
            >
              {{ $t('common.delete') }}
            </NButton>
          </div>
        </NFormItem>
        <NFormItem :label="text('links')">
          <div class="w-full flex-col gap-6px">
            <div v-for="(item, position) in form.links" :key="position" class="flex gap-6px">
              <NInput v-model:value="item.title" size="small" :maxlength="200" :placeholder="text('linkTitle')" />
              <NInput
                v-model:value="item.url"
                size="small"
                :maxlength="2048"
                :placeholder="urlPlaceholder"
                class="font-mono"
              />
              <NButton size="small" quaternary type="error" @click="form.links.splice(position, 1)">
                {{ $t('common.delete') }}
              </NButton>
            </div>
            <NButton size="small" :disabled="form.links.length >= 8" @click="form.links.push({ title: '', url: '' })">
              {{ text('addLink') }}
            </NButton>
          </div>
        </NFormItem>
        <NButton type="primary" :disabled="!valid" :loading="saving" @click="save">{{ text('save') }}</NButton>
      </NForm>
    </NModal>
  </div>
</template>
