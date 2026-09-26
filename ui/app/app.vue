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
    { rel: 'icon', href: '/favicon.ico', sizes: '32x32' },
    { rel: 'icon', href: '/icon.png', type: 'image/png', sizes: '192x192' },
    { rel: 'apple-touch-icon', href: '/apple-touch-icon.png' }
  ],
  htmlAttrs: {
    lang: 'en'
  }
})

useClaimAnonymousLinks()

useSeoMeta({
  ogSiteName: site.name,
  ogLocale: 'en_US',
  twitterCard: 'summary_large_image',
  twitterCreator: site.xHandle
})
</script>

<template>
  <UApp :toaster="null">
    <div class="flex min-h-dvh flex-col">
      <AppHeader />
      <main class="flex-1">
        <NuxtPage />
      </main>
      <AppFooter />
    </div>
    <Teleport to="#teleports">
      <Toaster
        position="bottom-center"
        :theme="colorMode.value === 'dark' ? 'dark' : 'light'"
      />
    </Teleport>
  </UApp>
</template>
