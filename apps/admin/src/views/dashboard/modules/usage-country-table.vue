<script setup lang="ts">
import { computed } from 'vue';
import { formatCount } from '@opennavo/shared';
import type { Schemas } from '@/typings/api/opennavo';
import { $t } from '@/locales';

const props = defineProps<{
  countries: NonNullable<Schemas['UsageOverview']['countries']>;
  locale: App.I18n.LangType;
}>();
const countryNames = computed(() => new Intl.DisplayNames([props.locale], { type: 'region' }));
const countryName = (code: string) =>
  code === 'ZZ' ? $t('page.dashboard.usage.unknownCountry') : (countryNames.value.of(code) ?? code);
</script>

<template>
  <div class="overflow-x-auto">
    <NTable size="small" :bordered="false" class="tabular-nums">
      <thead>
        <tr>
          <th scope="col">{{ $t('page.dashboard.usage.country') }}</th>
          <th scope="col" class="!text-right">{{ $t('page.dashboard.usage.desktop') }}</th>
          <th scope="col" class="!text-right">{{ $t('page.dashboard.usage.web') }}</th>
        </tr>
      </thead>
      <tbody>
        <tr v-for="row in countries" :key="row.country">
          <th scope="row" class="!bg-transparent !font-normal">{{ countryName(row.country) }}</th>
          <td class="text-right">{{ formatCount(row.desktop, { locale }) }}</td>
          <td class="text-right">{{ formatCount(row.web, { locale }) }}</td>
        </tr>
      </tbody>
    </NTable>
  </div>
</template>
