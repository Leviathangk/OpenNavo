<script setup lang="ts">
import { RouterLink } from 'vue-router';
import { useI18n } from 'vue-i18n';
import { OnBreadcrumb, OnPackageHeader } from '@opennavo/ui';
import PreviewNotice from '@/components/common/PreviewNotice.vue';
import { commands, unwrap } from '@/ipc/client';
import { useLoader } from '@/composables/useLoader';

// ADR-018: legacy formula links remain informational; this page exposes no write actions.
const props = defineProps<{ token: string }>();
const { t } = useI18n();
const { data } = useLoader(() => unwrap(commands.catalogGet('formula', props.token)), [() => props.token]);
</script>

<template>
  <div class="flex flex-col gap-14px pt-6px">
    <OnBreadcrumb :items="[{ label: t('nav.discover'), href: '/discover' }, { label: token }]" :link-as="RouterLink" />
    <OnPackageHeader kind="formula" :token="token" :name="data?.name ?? token" />
    <PreviewNotice :title="t('deeplink.formulaUnsupported')" />
  </div>
</template>
