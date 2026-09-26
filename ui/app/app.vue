<script setup lang="ts">
const colorMode = useColorMode()
const themeColor = computed(() => colorMode.value === 'dark' ? '#171715' : '#f7f4eb')

if (import.meta.client) {
  watch(themeColor, (value) => {
    document.querySelector('meta[name="theme-color"]')?.setAttribute('content', value)
  }, { immediate: true })
}

useHead({
  meta: [
    { name: 'viewport', content: 'width=device-width, initial-scale=1' },
    { key: 'theme-color', name: 'theme-color', content: themeColor }
  ],
  link: [
    { rel: 'icon', href: '/favicon.ico' }
  ],
  htmlAttrs: {
    lang: 'en'
  }
})

useSeoMeta({
  ogImage: 'https://ui.nuxt.com/assets/templates/nuxt/landing-light.png',
  twitterCard: 'summary_large_image'
})
</script>

<template>
  <UApp :toaster="{ expand: false }">
    <AppHeader />

    <UMain>
      <NuxtPage />
    </UMain>

    <AppFooter />
  </UApp>
</template>
