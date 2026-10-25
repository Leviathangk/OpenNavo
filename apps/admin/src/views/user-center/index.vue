<script setup lang="ts">
import { computed, reactive, ref } from 'vue';
import type { FormInst, FormRules } from 'naive-ui';
import { changePassword, fetchProfile, updateProfile } from '@/service/api';
import { useAuthStore } from '@/store/modules/auth';
import { REG_EMAIL } from '@/constants/reg';
import { $t } from '@/locales';
import OAuthGrants from './modules/oauth-grants.vue';

defineOptions({ name: 'UserCenter' });

// Profile (07 §7.15): account details/password changes; backend invalidates old tokens after password changes, so notify then log out.
const authStore = useAuthStore();

const profileRef = ref<FormInst | null>(null);
const passwordRef = ref<FormInst | null>(null);
const profile = reactive({ nickName: '', email: '' as string | null, avatarUrl: '' as string | null });
const password = reactive({ oldPassword: '', newPassword: '', confirm: '' });
const savingProfile = ref(false);
const savingPassword = ref(false);
const userName = ref('');

void fetchProfile().then(({ data, error }) => {
  if (error) return;
  userName.value = data.userName;
  Object.assign(profile, { nickName: data.nickName, email: data.email ?? '', avatarUrl: data.avatarUrl ?? '' });
});

const profileRules = computed<FormRules>(() => ({
  nickName: [{ required: true, message: $t('form.required'), trigger: ['blur', 'input'] }],
  email: [{ pattern: REG_EMAIL, message: $t('form.email.invalid'), trigger: ['blur', 'input'] }]
}));

const passwordRules = computed<FormRules>(() => ({
  oldPassword: [{ required: true, message: $t('form.required'), trigger: ['blur', 'input'] }],
  newPassword: [
    { required: true, message: $t('form.required'), trigger: ['blur', 'input'] },
    { min: 10, message: $t('page.userCenter.passwordInvalid'), trigger: ['blur', 'input'] }
  ],
  confirm: [
    { required: true, message: $t('form.required'), trigger: ['blur', 'input'] },
    {
      validator: (_rule, value: string) => value === password.newPassword,
      message: $t('page.userCenter.mismatch'),
      trigger: ['blur', 'input']
    }
  ]
}));

const blankToNull = (value: string | null) => (value?.trim() ? value.trim() : null);

async function saveProfile() {
  await profileRef.value?.validate();
  savingProfile.value = true;
  const { error } = await updateProfile({
    nickName: profile.nickName.trim(),
    email: blankToNull(profile.email),
    avatarUrl: blankToNull(profile.avatarUrl)
  });
  savingProfile.value = false;
  if (!error) window.$message?.success($t('page.shared.saved'));
}

async function savePassword() {
  await passwordRef.value?.validate();
  savingPassword.value = true;
  const { error } = await changePassword({ oldPassword: password.oldPassword, newPassword: password.newPassword });
  savingPassword.value = false;
  if (error) return;
  window.$message?.success($t('page.userCenter.passwordChanged'));
  await authStore.resetStore();
}
</script>

<template>
  <NGrid :x-gap="16" :y-gap="16" responsive="screen" item-responsive>
    <NGi span="24 m:12">
      <NCard :title="$t('page.userCenter.profile')" :bordered="false" size="small" class="card-wrapper h-full">
        <div class="mb-16px flex items-center gap-12px">
          <NAvatar :size="48" :src="profile.avatarUrl || undefined" round>
            {{ (profile.nickName || userName).slice(0, 1) }}
          </NAvatar>
          <div class="flex-col">
            <span class="font-600">{{ profile.nickName || userName }}</span>
            <NText depth="3" class="font-mono text-12px">{{ userName }}</NText>
          </div>
        </div>
        <NForm ref="profileRef" :model="profile" :rules="profileRules" label-placement="left" :label-width="88">
          <NFormItem :label="$t('page.userCenter.nickName')" path="nickName">
            <NInput v-model:value="profile.nickName" :maxlength="64" />
          </NFormItem>
          <NFormItem :label="$t('page.userCenter.email')" path="email">
            <NInput v-model:value="profile.email" clearable />
          </NFormItem>
          <NFormItem :label="$t('page.userCenter.avatarUrl')" path="avatarUrl">
            <NInput v-model:value="profile.avatarUrl" placeholder="https://" clearable />
          </NFormItem>
        </NForm>
        <div class="flex justify-end">
          <NButton type="primary" :loading="savingProfile" @click="saveProfile">
            {{ $t('page.userCenter.save') }}
          </NButton>
        </div>
      </NCard>
    </NGi>
    <NGi span="24 m:12">
      <NCard :title="$t('page.userCenter.password')" :bordered="false" size="small" class="card-wrapper h-full">
        <NForm ref="passwordRef" :model="password" :rules="passwordRules" label-placement="left" :label-width="100">
          <NFormItem :label="$t('page.userCenter.oldPassword')" path="oldPassword">
            <NInput
              v-model:value="password.oldPassword"
              type="password"
              show-password-on="click"
              autocomplete="current-password"
            />
          </NFormItem>
          <NFormItem :label="$t('page.userCenter.newPassword')" path="newPassword">
            <NInput
              v-model:value="password.newPassword"
              type="password"
              show-password-on="click"
              :maxlength="128"
              autocomplete="new-password"
            />
          </NFormItem>
          <NFormItem :label="$t('page.userCenter.confirmPassword')" path="confirm">
            <NInput
              v-model:value="password.confirm"
              type="password"
              show-password-on="click"
              :maxlength="128"
              autocomplete="new-password"
            />
          </NFormItem>
        </NForm>
        <div class="flex justify-end">
          <NButton type="primary" :loading="savingPassword" @click="savePassword">
            {{ $t('page.userCenter.changePassword') }}
          </NButton>
        </div>
      </NCard>
    </NGi>
    <NGi span="24"><OAuthGrants /></NGi>
  </NGrid>
</template>
