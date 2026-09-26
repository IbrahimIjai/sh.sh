<script setup lang="ts">
import { toast } from 'vue-sonner'

const route = useRoute()
const router = useRouter()
const { siteUrl } = useRuntimeConfig().public
const pageUrl = `${siteUrl}/`
const ogImage = `${siteUrl}/og.png`

useSeoMeta({
  title: site.title,
  description: site.description,
  ogType: 'website',
  ogUrl: pageUrl,
  ogTitle: site.tagline,
  ogDescription: `${site.pitch} Free, with no account needed.`,
  ogImage,
  ogImageWidth: 1200,
  ogImageHeight: 630,
  ogImageAlt: `${site.name}: ${site.tagline}`,
  twitterTitle: site.tagline,
  twitterDescription: `${site.pitch} Free, with no account needed.`,
  twitterImage: ogImage
})

useHead({
  link: [{ rel: 'canonical', href: pageUrl }],
  script: [{
    type: 'application/ld+json',
    textContent: JSON.stringify({
      '@context': 'https://schema.org',
      '@graph': [
        {
          '@type': 'WebSite',
          '@id': `${pageUrl}#website`,
          'name': site.name,
          'url': pageUrl
        },
        {
          '@type': 'WebApplication',
          'name': site.name,
          'url': pageUrl,
          'description': site.description,
          'applicationCategory': 'UtilitiesApplication',
          'operatingSystem': 'Any',
          'browserRequirements': 'Requires JavaScript',
          'offers': [
            { '@type': 'Offer', 'name': 'Free', 'price': 0, 'priceCurrency': 'NGN' },
            { '@type': 'Offer', 'name': 'Pro', 'price': site.priceNaira, 'priceCurrency': 'NGN' }
          ],
          'creator': {
            '@type': 'Person',
            'name': site.author,
            'sameAs': [site.x, site.githubProfile]
          }
        }
      ]
    })
  }]
})

onMounted(() => {
  if (typeof route.query.error === 'string') {
    navigateTo({ path: '/login', query: { error: route.query.error } }, { replace: true })
  }
})

onNuxtReady(() => {
  if (route.query.login !== 'success') return
  toast.success('You are logged in.')
  router.replace({ query: {} })
})
</script>

<template>
  <div>
    <section class="mx-auto w-full max-w-2xl px-5 pt-[9vh] text-center sm:px-8 sm:pt-[15vh]">
      <h1 class="text-[clamp(42px,6vw,72px)] leading-[1.02] font-semibold tracking-[-0.04em] text-balance text-highlighted">
        {{ site.tagline }}
      </h1>
      <p class="mx-auto mt-5 max-w-md text-[15px] leading-relaxed text-toned">
        {{ site.pitch }}
      </p>
      <ShortenCard class="mt-10" />
    </section>

    <UpgradeInvite class="mt-20 sm:mt-28" />
  </div>
</template>
