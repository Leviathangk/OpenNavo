<script setup lang="ts">
import { OnChip } from '../../../src';
import StoryRow from '../StoryRow.vue';
import StorySection from '../StorySection.vue';
</script>

<template>
  <StorySection title="OnChip chips" spec="08 §8.3" description="Always express state in text; color is supplementary.">
    <StoryRow label="Type">
      <OnChip>App</OnChip>
      <OnChip>Command line</OnChip>
      <OnChip icon="lucide:pin">Pinned</OnChip>
    </StoryRow>
    <StoryRow label="State">
      <OnChip tone="success" dot>Installed 1.140.0</OnChip>
      <OnChip tone="success" dot>Latest</OnChip>
      <OnChip tone="accent" dot>Update available</OnChip>
      <OnChip tone="coral" dot>Updating 62%</OnChip>
      <OnChip tone="info">Built-in updater</OnChip>
      <OnChip tone="warning">Requires administrator privileges</OnChip>
      <OnChip tone="danger">Deprecated</OnChip>
      <OnChip tone="danger" outline>Disabled</OnChip>
    </StoryRow>
    <StoryRow label="Outline">
      <OnChip tone="outline">Patch</OnChip>
    </StoryRow>
    <StoryRow label="Medium size">
      <OnChip size="md">App</OnChip>
      <OnChip size="md" tone="success" dot>Installed</OnChip>
      <OnChip size="md" tone="accent" dot>Update available</OnChip>
      <OnChip size="md" tone="info" icon="lucide:refresh-cw">Built-in updater</OnChip>
    </StoryRow>
  </StorySection>
</template>
