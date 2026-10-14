<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue';
import type { ReleaseEntry } from '@opennavo/api';
import { OnVersionEntry } from '@opennavo/ui';
import { OnMarkdown } from '@opennavo/ui/markdown';
import { fetchReleaseDetail, updateReleaseI18n } from '@/service/api';
import type { DataOf } from '@/typings/api/opennavo';
import { useAuth } from '@/hooks/business/auth';
import { localeName } from '@/utils/content-locale';
import type { ContentLocale } from '@/utils/content-locale';
import { $t } from '@/locales';
import MarkdownEditor from './markdown-editor.vue';
import UiPreview from './ui-preview.vue';

defineOptions({ name: 'ReleaseEditor' });

// Release translation drawer (07 §7.7, width 960): read-only source left, selected-language summary/highlights/body right.
// Public OnVersionEntry preview below; saving immediately creates a manual correction (12: no review; translation:review).
const props = withDefaults(defineProps<{ releaseId: number | null; locale?: ContentLocale }>(), { locale: 'en-US' });
const show = defineModel<boolean>('show', { required: true });
const emit = defineEmits<{ saved: [] }>();

const MAX_SECTIONS = 8;
const MAX_ITEMS = 6;

const { hasAuth } = useAuth();
const editable = computed(() => hasAuth('translation:review'));
const detail = ref<DataOf<'getAdminRelease'> | null>(null);
const loading = ref(false);
const saving = ref(false);
const form = reactive({ summary: '', body: '', sections: [] as { area: string; items: string[] }[] });

const translation = computed(() => detail.value?.i18n.find(item => item.locale === props.locale) ?? null);

const ready = computed(() => show.value && !loading.value && detail.value?.id === props.releaseId);
watch(
  [show, () => props.releaseId, () => props.locale],
  async ([open, id, locale], _previous, onCleanup) => {
    let current = true;
    onCleanup(() => {
      current = false;
    });
    detail.value = null;
    form.summary = '';
    form.body = '';
    form.sections = [];
    loading.value = open && id !== null;
    if (!open || id === null) return;
    try {
      const { data, error } = await fetchReleaseDetail(id);
      if (!current || error || data.id !== id) return;
      detail.value = data;
      const view = data.i18n.find(item => item.locale === locale);
      form.summary = view?.summary ?? '';
      form.body = view?.bodyMarkdown ?? '';
      form.sections = (view?.sections ?? []).map(section => ({ area: section.area, items: [...section.items] }));
    } finally {
      if (current) loading.value = false;
    }
  },
  { immediate: true, flush: 'sync' }
);

function addSection() {
  if (form.sections.length >= MAX_SECTIONS) {
    window.$message?.warning($t('page.changelog.editor.maxSections'));
    return;
  }
  form.sections.push({ area: '', items: [''] });
}

function addItem(index: number) {
  const section = form.sections[index];
  if (!section) return;
  if (section.items.length >= MAX_ITEMS) {
    window.$message?.warning($t('page.changelog.editor.maxItems'));
    return;
  }
  section.items.push('');
}

const cleanSections = () =>
  form.sections
    .map(section => ({ area: section.area.trim(), items: section.items.map(item => item.trim()).filter(Boolean) }))
    .filter(section => section.area && section.items.length);

async function save() {
  if (!editable.value || !ready.value || saving.value || !detail.value) return;
  const id = detail.value.id;
  const locale = props.locale;
  saving.value = true;
  try {
    const { error } = await updateReleaseI18n(id, locale, {
      summary: form.summary.trim() || null,
      sections: cleanSections(),
      bodyMarkdown: form.body.trim() || null
    });
    if (error) return;
    window.$message?.success($t('page.changelog.editor.saved'));
    if (props.releaseId === id && props.locale === locale) show.value = false;
    emit('saved');
  } finally {
    saving.value = false;
  }
}

// Preview: build a public release entry from the current form.
const preview = computed<ReleaseEntry | null>(() => {
  const release = detail.value;
  if (!release) return null;
  const sections = cleanSections();
  return {
    id: release.id,
    // Notes owned by one build preview with its build tag, as the public timeline shows them.
    version: release.buildVersion ?? release.version,
    versionBase: release.version,
    build: release.buildVersion ? release.buildVersion.slice(release.version.length + 1) : undefined,
    title: release.title ?? null,
    publishedAt: release.publishedAt ?? null,
    brewCommittedAt: null,
    source: release.source,
    sourceUrl: release.sourceUrl ?? null,
    isLatest: false,
    isPrerelease: release.isPrerelease,
    hasNotes: Boolean(sections.length || form.body.trim() || form.summary.trim()),
    summary: form.summary.trim() || null,
    sections,
    bodyMarkdown: form.body.trim() || null,
    translation: { status: 'manual', locale: props.locale }
  };
});
</script>

<template>
  <NDrawer v-model:show="show" :width="960" placement="right">
    <NDrawerContent :title="detail ? `${detail.packageName} ${detail.buildVersion ?? detail.version}` : ''" closable :native-scrollbar="false">
      <NSpin :show="loading">
        <div v-if="detail" class="flex-col gap-16px">
          <NAlert v-if="translation?.failReason" type="error" :bordered="false">
            {{ $t('page.changelog.editor.failReason', { reason: translation.failReason }) }}
          </NAlert>
          <NAlert v-if="translation?.stale" type="warning" :bordered="false">{{ $t('page.changelog.editor.stale') }}</NAlert>
          <NGrid :x-gap="16" :cols="2">
            <NGi>
              <div class="flex-col gap-8px">
                <h4 class="m-0 text-14px font-600">{{ $t('page.changelog.editor.original') }}</h4>
                <UiPreview>
                  <div class="max-h-560px overflow-auto">
                    <OnMarkdown v-if="detail.bodyMarkdown" :source="detail.bodyMarkdown" :heading-level="3" />
                    <span v-else class="text-13px text-ink-tertiary">{{ $t('page.changelog.editor.noOriginal') }}</span>
                  </div>
                </UiPreview>
              </div>
            </NGi>
            <NGi>
              <div class="flex-col gap-8px">
                <h4 class="m-0 text-14px font-600">
                  {{ $t('page.changelog.editor.translation') }} · {{ localeName(props.locale) }}
                </h4>
                <NForm :disabled="!editable || !ready || saving" label-placement="top" size="small">
                  <NFormItem :label="$t('page.changelog.editor.summary')">
                    <NInput v-model:value="form.summary" :maxlength="160" show-count clearable />
                  </NFormItem>
                  <NFormItem :label="$t('page.changelog.editor.sections')">
                    <div class="w-full flex-col gap-8px">
                      <NCard v-for="(section, index) in form.sections" :key="index" size="small" embedded>
                        <div class="flex items-center gap-8px">
                          <NInput
                            v-model:value="section.area"
                            :maxlength="12"
                            :placeholder="$t('page.changelog.editor.area')"
                            class="w-140px"
                          />
                          <NButton size="tiny" quaternary @click="addItem(index)">
                            {{ $t('page.changelog.editor.addItem') }}
                          </NButton>
                          <NButton
                            size="tiny"
                            quaternary
                            type="error"
                            class="ml-auto"
                            @click="form.sections.splice(index, 1)"
                          >
                            {{ $t('common.delete') }}
                          </NButton>
                        </div>
                        <div class="mt-8px flex-col gap-6px">
                          <div v-for="(_, item) in section.items" :key="item" class="flex items-center gap-6px">
                            <NInput
                              v-model:value="section.items[item]"
                              :maxlength="80"
                              :placeholder="$t('page.changelog.editor.item')"
                            />
                            <NButton size="tiny" quaternary @click="section.items.splice(item, 1)">×</NButton>
                          </div>
                        </div>
                      </NCard>
                      <NButton size="small" dashed @click="addSection">
                        {{ $t('page.changelog.editor.addSection') }}
                      </NButton>
                    </div>
                  </NFormItem>
                  <NFormItem :label="$t('page.changelog.editor.body')">
                    <MarkdownEditor v-model="form.body" :maxlength="200000" :rows="8" :disabled="!editable" />
                  </NFormItem>
                </NForm>
              </div>
            </NGi>
          </NGrid>
          <div v-if="preview" class="flex-col gap-8px">
            <h4 class="m-0 text-14px font-600">{{ $t('page.changelog.editor.preview') }}</h4>
            <UiPreview>
              <OnVersionEntry :entry="preview">
                <template #markdown="{ source }">
                  <OnMarkdown :source="source" :heading-level="4" />
                </template>
              </OnVersionEntry>
            </UiPreview>
          </div>
        </div>
      </NSpin>
      <template #footer>
        <div class="flex justify-end gap-12px">
          <NButton @click="show = false">{{ $t('common.cancel') }}</NButton>
          <PermissionGate code="translation:review">
            <NButton type="primary" :loading="saving" :disabled="!ready" @click="save">
              {{ $t('page.changelog.editor.save') }}
            </NButton>
          </PermissionGate>
        </div>
      </template>
    </NDrawerContent>
  </NDrawer>
</template>
