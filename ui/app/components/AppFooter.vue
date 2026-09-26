<script setup lang="ts">
const links = [
  { label: 'Docs', to: 'https://ui.nuxt.com', target: '_blank' },
  { label: 'GitHub', to: 'https://github.com/nuxt/ui', target: '_blank' },
  { label: 'Twitter', to: 'https://twitter.com/nuxt_js', target: '_blank' },
  { label: 'Status', to: '#' },
  { label: 'Privacy', to: '#' }
]
</script>

<template>
  <UFooter
    :ui="{
      container: 'max-w-6xl border-t border-muted lg:py-6',
      right: 'gap-x-0 flex-wrap'
    }"
  >
    <template #left>
      <p class="text-xs text-toned">
        Built with Nuxt UI • © {{ new Date().getFullYear() }}
      </p>
    </template>

    <template #right>
      <UButton
        v-for="link in links"
        :key="link.label"
        :label="link.label"
        :to="link.to"
        color="neutral"
        variant="link"
        class="font-light"
        size="sm"
      />
    </template>
  </UFooter>
</template>
