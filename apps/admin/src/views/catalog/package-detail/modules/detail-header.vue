<script setup lang="ts">
import { computed as computedFormattingLocale } from 'vue';
import { useAppStore as useFormattingAppStore } from '@/store/modules/app';
import { formatCount } from '@opennavo/shared';
import type { DataOf } from '@/typings/api/opennavo';
import PackageIcon from '@/components/opennavo/package-icon.vue';
import { useLocalizedPackage } from '@/hooks/business/localized';
import { $t } from '@/locales';

const formattingAppStore = useFormattingAppStore();
const formattingLocale = computedFormattingLocale(() => formattingAppStore.locale);

defineOptions({ name: 'DetailHeader' });

defineProps<{ detail: DataOf<'getAdminPackage'> }>();
const { nameOf } = useLocalizedPackage();
</script>

<template>
  <NCard :bordered="false" size="small" class="card-wrapper">
    <div class="flex flex-wrap items-center gap-16px">
      <PackageIcon :url="detail.iconUrl" :name="nameOf(detail)" :kind="detail.kind" :size="56" />
      <div class="min-w-0 flex-col gap-4px">
        <div class="flex flex-wrap items-center gap-8px">
          <span class="text-20px font-600">{{ nameOf(detail) }}</span>
          <NTag v-if="detail.private" size="small" type="warning" :bordered="false">
            {{ $t('page.catalog.packageDetail.basic.private') }}
          </NTag>
          <NTag v-if="detail.hidden" size="small" :bordered="false">{{ $t('page.shared.flags.hidden') }}</NTag>
          <NTag v-if="detail.editorChoice" size="small" type="primary" :bordered="false">
            {{ $t('page.shared.flags.editorChoice') }}
          </NTag>
          <NTag v-if="detail.deprecated" size="small" type="warning" :bordered="false">
            {{ $t('page.shared.flags.deprecated') }}
          </NTag>
          <NTag v-if="detail.disabled" size="small" type="error" :bordered="false">
            {{ $t('page.shared.flags.disabled') }}
          </NTag>
        </div>
        <NText depth="3" class="flex flex-wrap items-center gap-12px text-13px">
          <span class="font-mono">{{ detail.token }}</span>
          <span class="font-mono">{{ detail.version }}</span>
          <span>
            {{
              $t(
                'page.catalog.packageDetail.installs30d',
                { count: formatCount(detail.installs30d, { locale: formattingLocale }) },
                { plural: detail.installs30d }
              )
            }}
          </span>
          <span v-if="detail.rank30d">#{{ detail.rank30d }}</span>
        </NText>
      </div>
      <!-- Backend constructs public URLs from WEB_BASE_URL; hidden/private/removed packages show no link. -->
      <NButton
        v-if="detail.webUrl"
        tag="a"
        :href="detail.webUrl"
        target="_blank"
        rel="noopener noreferrer"
        size="small"
        secondary
        class="ml-auto"
      >
        {{ $t('page.shared.viewOnSite') }} ↗
      </NButton>
    </div>
  </NCard>
</template>
