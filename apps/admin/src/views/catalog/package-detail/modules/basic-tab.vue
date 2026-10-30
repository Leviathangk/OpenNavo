<script setup lang="ts">
import { computed as computedFormattingLocale } from 'vue';
import { useAppStore as useFormattingAppStore } from '@/store/modules/app';
import { computed, reactive, ref, watch } from 'vue';
import { formatBytes } from '@opennavo/shared';
import { updatePackageMeta } from '@/service/api';
import type { BodyOf, DataOf } from '@/typings/api/opennavo';
import { useAuth } from '@/hooks/business/auth';
import { $t } from '@/locales';

const formattingAppStore = useFormattingAppStore();
const formattingLocale = computedFormattingLocale(() => formattingAppStore.locale);

defineOptions({ name: 'BasicTab' });

const props = defineProps<{ detail: DataOf<'getAdminPackage'> }>();
const emit = defineEmits<{ saved: [] }>();

const { hasAuth } = useAuth();
const editable = computed(() => hasAuth('catalog:package:edit'));

type Meta = Required<BodyOf<'updatePackageMeta'>>;
const model = reactive<Meta>({
  downloadSize: null,
  accentColor: null,
  developer: null,
  repoUrl: null,
  hidden: false,
  private: false,
  visibilityReason: '',
  editorChoice: false,
  tags: [],
  notes: null
});
const saving = ref(false);
// Size is automatically probed and accent derived from icons; submit only fields edited since loading so stale values cannot overwrite new results.
const loaded = { downloadSize: null as number | null, accentColor: null as string | null };

watch(
  () => props.detail,
  detail => {
    loaded.downloadSize = detail.downloadSize ?? null;
    loaded.accentColor = detail.meta.accentColor ?? null;
    Object.assign(model, {
      downloadSize: loaded.downloadSize,
      accentColor: loaded.accentColor,
      developer: detail.meta.developer ?? null,
      repoUrl: detail.meta.repoUrl ?? null,
      hidden: detail.hidden,
      private: detail.private ?? false,
      visibilityReason: detail.visibilityReason ?? '',
      editorChoice: detail.editorChoice,
      tags: [...(detail.meta.tags ?? [])],
      notes: detail.meta.notes ?? null
    });
  },
  { immediate: true }
);

const raw = computed(() => JSON.stringify(props.detail.raw, null, 2));
const reasonRequired = computed(() => model.hidden || model.private);
const reasonInvalid = computed(() => reasonRequired.value && !model.visibilityReason.trim());

// Submit empty strings as null, which the backend treats as unfilled.
const blankToNull = (value: string | null) => (value?.trim() ? value.trim() : null);

async function save() {
  if (reasonInvalid.value) return;
  saving.value = true;
  const body: BodyOf<'updatePackageMeta'> = {
    developer: blankToNull(model.developer),
    repoUrl: blankToNull(model.repoUrl),
    hidden: model.hidden,
    private: model.private,
    editorChoice: model.editorChoice,
    notes: blankToNull(model.notes),
    tags: model.tags.map(tag => tag.trim()).filter(Boolean)
  };
  if (model.visibilityReason.trim()) body.visibilityReason = model.visibilityReason.trim();
  if (model.downloadSize !== loaded.downloadSize) body.downloadSize = model.downloadSize;
  if (model.accentColor !== loaded.accentColor) body.accentColor = model.accentColor?.toUpperCase() ?? null;
  const { error } = await updatePackageMeta(props.detail.id, body);
  saving.value = false;
  if (error) return;
  window.$message?.success($t('page.shared.saved'));
  emit('saved');
}
</script>

<template>
  <NGrid :x-gap="24" :y-gap="16" responsive="screen" item-responsive>
    <NGi span="24 l:12">
      <div class="flex-col gap-12px">
        <div class="flex items-baseline gap-8px">
          <h3 class="m-0 text-15px font-600">{{ $t('page.catalog.packageDetail.basic.homebrew') }}</h3>
          <NText depth="3" class="text-12px">{{ $t('page.catalog.packageDetail.basic.homebrewHint') }}</NText>
        </div>
        <NDescriptions :column="1" label-placement="left" bordered size="small" label-class="w-120px whitespace-nowrap">
          <NDescriptionsItem :label="$t('page.catalog.packageDetail.basic.names')">
            {{ detail.names.join(' · ') || '—' }}
          </NDescriptionsItem>
          <NDescriptionsItem :label="$t('page.catalog.packageDetail.basic.descEn')">
            {{ detail.descEn || '—' }}
          </NDescriptionsItem>
          <NDescriptionsItem :label="$t('page.catalog.packageDetail.basic.homepage')">
            <a
              v-if="detail.homepage"
              :href="detail.homepage"
              target="_blank"
              rel="noopener noreferrer"
              class="text-primary"
            >
              {{ detail.homepage }}
            </a>
            <template v-else>—</template>
          </NDescriptionsItem>
          <NDescriptionsItem :label="$t('page.catalog.packageDetail.basic.downloadUrl')">
            <NEllipsis v-if="detail.downloadUrl" class="max-w-480px">
              <a :href="detail.downloadUrl" target="_blank" rel="noopener noreferrer" class="text-primary">
                {{ detail.downloadUrl }}
              </a>
            </NEllipsis>
            <template v-else>—</template>
          </NDescriptionsItem>
          <NDescriptionsItem :label="$t('page.catalog.packageDetail.basic.tap')">
            <span class="font-mono">{{ detail.tap }}</span>
          </NDescriptionsItem>
        </NDescriptions>
        <NCollapse>
          <NCollapseItem :title="$t('page.catalog.packageDetail.basic.raw')" name="raw">
            <pre class="m-0 max-h-420px overflow-auto text-12px leading-[1.6]"><code>{{ raw }}</code></pre>
          </NCollapseItem>
        </NCollapse>
      </div>
    </NGi>
    <NGi span="24 l:12">
      <div class="flex-col gap-12px">
        <h3 class="m-0 text-15px font-600">{{ $t('page.catalog.packageDetail.basic.editable') }}</h3>
        <NForm :model="model" :disabled="!editable" label-placement="left" :label-width="96">
          <NFormItem :label="$t('page.catalog.packageDetail.basic.developer')" path="developer">
            <NInput v-model:value="model.developer" :maxlength="120" clearable />
          </NFormItem>
          <NFormItem :label="$t('page.catalog.packageDetail.basic.repoUrl')" path="repoUrl">
            <NInput
              v-model:value="model.repoUrl"
              :maxlength="300"
              placeholder="https://github.com/owner/repo"
              clearable
            />
          </NFormItem>
          <NFormItem :label="$t('page.catalog.packageDetail.basic.downloadSize')" path="downloadSize">
            <div class="w-full flex-col gap-4px">
              <div class="flex items-center gap-8px">
                <NInputNumber
                  v-model:value="model.downloadSize"
                  :min="0"
                  :precision="0"
                  :show-button="false"
                  :placeholder="$t('page.catalog.packageDetail.basic.downloadSizeUnit')"
                  clearable
                  class="w-200px"
                />
                <NText v-if="model.downloadSize" depth="3" class="text-12px">
                  {{ formatBytes(model.downloadSize, { locale: formattingLocale }) }}
                </NText>
              </div>
              <NText depth="3" class="text-12px">{{ $t('page.catalog.packageDetail.basic.downloadSizeHint') }}</NText>
            </div>
          </NFormItem>
          <NFormItem :label="$t('page.catalog.packageDetail.basic.accentColor')" path="accentColor">
            <div class="w-full flex-col gap-4px">
              <div class="flex items-center gap-8px">
                <div class="w-160px">
                  <NColorPicker v-model:value="model.accentColor" :modes="['hex']" :show-alpha="false" />
                </div>
                <NButton v-if="model.accentColor" size="small" quaternary @click="model.accentColor = null">
                  {{ $t('page.catalog.packageDetail.basic.accentColorClear') }}
                </NButton>
              </div>
              <NText depth="3" class="text-12px">{{ $t('page.catalog.packageDetail.basic.accentColorHint') }}</NText>
            </div>
          </NFormItem>
          <NFormItem :label="$t('page.catalog.packageDetail.basic.tags')" path="tags">
            <div class="flex-col gap-4px">
              <NDynamicTags v-model:value="model.tags" :max="10" :input-props="{ maxlength: 20 }" />
              <NText depth="3" class="text-12px">{{ $t('page.catalog.packageDetail.basic.tagsHint') }}</NText>
            </div>
          </NFormItem>
          <NFormItem :label="$t('page.catalog.packageDetail.basic.hidden')" path="hidden">
            <div class="flex items-center gap-12px">
              <NSwitch v-model:value="model.hidden" />
              <NText depth="3" class="text-12px">{{ $t('page.catalog.packageDetail.basic.hiddenHint') }}</NText>
            </div>
          </NFormItem>
          <NFormItem :label="$t('page.catalog.packageDetail.basic.private')" path="private">
            <div class="flex items-center gap-12px">
              <NSwitch v-model:value="model.private" />
              <NText depth="3" class="text-12px">{{ $t('page.catalog.packageDetail.basic.privateHint') }}</NText>
            </div>
          </NFormItem>
          <NFormItem
            v-if="reasonRequired || model.visibilityReason"
            :label="$t('page.catalog.packageDetail.basic.visibilityReason')"
            path="visibilityReason"
            :required="reasonRequired"
            :validation-status="reasonInvalid ? 'error' : undefined"
            :feedback="reasonInvalid ? $t('page.catalog.packageDetail.basic.visibilityReasonRequired') : undefined"
          >
            <NInput v-model:value="model.visibilityReason" type="textarea" :rows="2" :maxlength="2000" show-count />
          </NFormItem>
          <NFormItem :label="$t('page.catalog.packageDetail.basic.editorChoice')" path="editorChoice">
            <NSwitch v-model:value="model.editorChoice" />
          </NFormItem>
          <NFormItem :label="$t('page.catalog.packageDetail.basic.notes')" path="notes">
            <NInput v-model:value="model.notes" type="textarea" :rows="4" :maxlength="2000" show-count />
          </NFormItem>
        </NForm>
        <PermissionGate code="catalog:package:edit">
          <div class="flex justify-end">
            <NButton type="primary" :loading="saving" :disabled="reasonInvalid" @click="save">
              {{ $t('page.catalog.packageDetail.basic.save') }}
            </NButton>
          </div>
        </PermissionGate>
      </div>
    </NGi>
  </NGrid>
</template>
