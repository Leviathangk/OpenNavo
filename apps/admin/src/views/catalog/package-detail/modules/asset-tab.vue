<script setup lang="ts">
import { computed, ref, watch } from 'vue';
import type { UploadCustomRequestOptions } from 'naive-ui';
import { VueDraggable } from 'vue-draggable-plus';
import {
  addPackageScreenshot,
  deletePackageIcon,
  deletePackageScreenshot,
  reorderPackageScreenshots,
  setPackageIcon,
  updatePackageScreenshot,
  uploadAsset
} from '@/service/api';
import type { DataOf, Schemas } from '@/typings/api/opennavo';
import { useAuth } from '@/hooks/business/auth';
import { useLocalizedPackage } from '@/hooks/business/localized';
import PackageIcon from '@/components/opennavo/package-icon.vue';
import { CONTENT_LOCALE_OPTIONS, DEFAULT_SOURCE_LOCALE, fromView, localizedBody } from '@/utils/content-locale';
import type { ContentLocale, LocalizedTexts } from '@/utils/content-locale';
import { isDryRun } from '@/utils/opennavo';
import { $t } from '@/locales';

defineOptions({ name: 'AssetTab' });

const props = defineProps<{ detail: DataOf<'getAdminPackage'> }>();
const emit = defineEmits<{ saved: [] }>();

// Editable screenshots: edit one caption locale (source by default), AI translates others; original detects changes.
type Screenshot = Schemas['AdminScreenshot'] & {
  dirty?: boolean;
  editLocale: ContentLocale;
  caption: string;
  original: LocalizedTexts;
};

// Match uploadAsset limits: PNG/JPEG/WebP, ≤5 MB.
const ACCEPT = ['image/png', 'image/jpeg', 'image/webp'];
const MAX_BYTES = 5 * 1024 * 1024;

const { hasAuth } = useAuth();
const { nameOf } = useLocalizedPackage();
const editable = computed(() => hasAuth('catalog:asset:upload'));
const shots = ref<Screenshot[]>([]);
const busy = ref(false);

watch(
  () => props.detail.screenshots,
  list => {
    shots.value = [...list]
      .sort((a, b) => a.sort - b.sort)
      .map(item => {
        const original = fromView(item.i18n, ['caption']).texts;
        const editLocale = item.sourceLocale ?? DEFAULT_SOURCE_LOCALE;
        return { ...item, editLocale, caption: original[editLocale]?.caption ?? '', original };
      });
  },
  { immediate: true }
);

function check(file: File | null | undefined): file is File {
  if (!file) return false;
  if (!ACCEPT.includes(file.type)) {
    window.$message?.error($t('page.catalog.packageDetail.assets.badType'));
    return false;
  }
  if (file.size > MAX_BYTES) {
    window.$message?.error($t('page.catalog.packageDetail.assets.tooLarge'));
    return false;
  }
  return true;
}

async function uploadIcon({ file, onFinish, onError }: UploadCustomRequestOptions) {
  if (!check(file.file)) return onError();
  busy.value = true;
  const asset = await uploadAsset(file.file, 'icon');
  if (asset.error || isDryRun(asset.data)) {
    busy.value = false;
    return onError();
  }
  const result = await setPackageIcon(props.detail.id, asset.data.id);
  busy.value = false;
  if (result.error) return onError();
  onFinish();
  window.$message?.success($t('page.shared.saved'));
  emit('saved');
}

function removeIcon() {
  window.$dialog?.warning({
    title: $t('page.catalog.packageDetail.assets.removeIcon'),
    content: $t('page.catalog.packageDetail.assets.removeIconConfirm'),
    positiveText: $t('common.confirm'),
    negativeText: $t('common.cancel'),
    onPositiveClick: async () => {
      const { error } = await deletePackageIcon(props.detail.id);
      if (error) return;
      window.$message?.success($t('page.shared.saved'));
      emit('saved');
    }
  });
}

async function uploadScreenshot({ file, onFinish, onError }: UploadCustomRequestOptions) {
  if (!check(file.file)) return onError();
  busy.value = true;
  const asset = await uploadAsset(file.file, 'screenshot');
  if (asset.error || isDryRun(asset.data)) {
    busy.value = false;
    return onError();
  }
  // New screenshots have no caption: omit i18n/sourceLocale (04 §9.1).
  const result = await addPackageScreenshot(props.detail.id, { assetId: asset.data.id, theme: 'dark' });
  busy.value = false;
  if (result.error) return onError();
  onFinish();
  emit('saved');
}

async function saveOrder() {
  const { error } = await reorderPackageScreenshots(
    props.detail.id,
    shots.value.map(item => item.id)
  );
  if (!error) window.$message?.success($t('page.shared.saved'));
}

// Switch editor locale to that locale's existing caption.
function switchLocale(shot: Screenshot, code: ContentLocale) {
  shot.editLocale = code;
  shot.caption = shot.original[code]?.caption ?? '';
}

async function saveShot(shot: Screenshot) {
  const caption = shot.caption.trim();
  const hadCaption = Object.values(shot.original).some(entry => entry?.caption);
  // Submit the edited locale as source; without an existing or new caption, change only theme.
  const texts: LocalizedTexts = { ...shot.original, [shot.editLocale]: { caption: caption || null } };
  const { error } = await updatePackageScreenshot(
    props.detail.id,
    shot.id,
    shot.editLocale === 'zh-Hant' || (!caption && !hadCaption)
      ? { theme: shot.theme }
      : {
          theme: shot.theme,
          sourceLocale: shot.editLocale,
          i18n: localizedBody({
            texts,
            original: shot.original,
            sourceLocale: shot.editLocale,
            keys: ['caption'],
            entry: text => ({ caption: text.caption })
          })
        }
  );
  if (error) return;
  shot.dirty = false;
  shot.original = texts;
  window.$message?.success($t('page.shared.saved'));
}

function removeShot(shot: Screenshot) {
  window.$dialog?.warning({
    title: $t('common.delete'),
    content: $t('page.catalog.packageDetail.assets.deleteScreenshotConfirm'),
    positiveText: $t('common.confirm'),
    negativeText: $t('common.cancel'),
    onPositiveClick: async () => {
      const { error } = await deletePackageScreenshot(props.detail.id, shot.id);
      if (error) return;
      window.$message?.success($t('common.deleteSuccess'));
      emit('saved');
    }
  });
}

const themeOptions = computed(() =>
  (['dark', 'light'] as const).map(value => ({ value, label: $t(`page.catalog.packageDetail.assets.themes.${value}`) }))
);
</script>

<template>
  <div class="flex-col gap-24px">
    <section class="flex-col gap-12px">
      <h3 class="m-0 text-15px font-600">{{ $t('page.catalog.packageDetail.assets.icon') }}</h3>
      <div class="flex flex-wrap items-center gap-16px">
        <PackageIcon :url="detail.iconUrl" :name="nameOf(detail)" :kind="detail.kind" :size="96" />
        <div class="flex-col gap-8px">
          <NText depth="3" class="text-13px">
            <template v-if="detail.meta.iconSource">
              {{ $t(`page.catalog.packageDetail.assets.iconSources.${detail.meta.iconSource}`) }}
            </template>
            <template v-else-if="!detail.hasIcon">{{ $t('page.catalog.packageDetail.assets.noIcon') }}</template>
          </NText>
          <div v-if="editable" class="flex gap-8px">
            <NUpload :accept="ACCEPT.join(',')" :show-file-list="false" :custom-request="uploadIcon">
              <NButton :loading="busy">{{ $t('page.catalog.packageDetail.assets.uploadIcon') }}</NButton>
            </NUpload>
            <NButton v-if="detail.hasIcon" type="error" ghost @click="removeIcon">
              {{ $t('page.catalog.packageDetail.assets.removeIcon') }}
            </NButton>
          </div>
          <NText depth="3" class="text-12px">{{ $t('page.catalog.packageDetail.assets.uploadHint') }}</NText>
        </div>
      </div>
    </section>

    <section class="flex-col gap-12px">
      <div class="flex flex-wrap items-center gap-12px">
        <h3 class="m-0 text-15px font-600">{{ $t('page.catalog.packageDetail.assets.screenshots') }}</h3>
        <NText v-if="editable && shots.length > 1" depth="3" class="text-12px">
          {{ $t('page.catalog.packageDetail.assets.screenshotsHint') }}
        </NText>
        <NUpload
          v-if="editable"
          class="ml-auto w-auto"
          :accept="ACCEPT.join(',')"
          :show-file-list="false"
          :custom-request="uploadScreenshot"
        >
          <NButton type="primary" ghost :loading="busy">
            {{ $t('page.catalog.packageDetail.assets.uploadScreenshot') }}
          </NButton>
        </NUpload>
      </div>
      <NEmpty v-if="!shots.length" :description="$t('page.catalog.packageDetail.assets.noScreenshots')" />
      <VueDraggable
        v-else
        v-model="shots"
        :animation="150"
        :disabled="!editable"
        handle=".drag-handle"
        class="grid grid-cols-[repeat(auto-fill,minmax(280px,1fr))] gap-16px"
        @end="saveOrder"
      >
        <NCard v-for="shot in shots" :key="shot.id" size="small" embedded>
          <template #cover>
            <img
              :src="shot.url"
              :alt="shot.caption"
              class="drag-handle aspect-[16/10] w-full cursor-grab object-cover"
              loading="lazy"
            />
          </template>
          <NForm :model="shot" :disabled="!editable" label-placement="top" size="small" :show-feedback="false">
            <div class="flex-col gap-8px pt-8px">
              <NFormItem :label="$t('page.catalog.packageDetail.assets.caption')">
                <div class="w-full flex gap-6px">
                  <NSelect
                    :value="shot.editLocale"
                    :options="CONTENT_LOCALE_OPTIONS"
                    class="w-120px shrink-0"
                    @update:value="(code: ContentLocale) => switchLocale(shot, code)"
                  />
                  <NInput
                    v-model:value="shot.caption"
                    :readonly="shot.editLocale === 'zh-Hant'"
                    :maxlength="120"
                    clearable
                    @update:value="shot.dirty = true"
                  />
                </div>
              </NFormItem>
              <NFormItem :label="$t('page.catalog.packageDetail.assets.theme')">
                <NSelect v-model:value="shot.theme" :options="themeOptions" @update:value="shot.dirty = true" />
              </NFormItem>
            </div>
          </NForm>
          <template v-if="editable" #action>
            <div class="flex justify-end gap-8px">
              <NButton size="small" type="error" quaternary @click="removeShot(shot)">
                {{ $t('common.delete') }}
              </NButton>
              <NButton size="small" type="primary" :disabled="!shot.dirty" @click="saveShot(shot)">
                {{ $t('page.catalog.packageDetail.basic.save') }}
              </NButton>
            </div>
          </template>
        </NCard>
      </VueDraggable>
    </section>
  </div>
</template>
