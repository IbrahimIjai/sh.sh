<script setup lang="ts">
const { data: page } = await useAsyncData('index', () => queryCollection('content').first())
if (!page.value) {
  throw createError({ statusCode: 404, statusMessage: 'Page not found', fatal: true })
}

const title = page.value?.seo?.title || page.value?.title
const description = page.value?.seo?.description || page.value?.description

useSeoMeta({
  title,
  ogTitle: title,
  description,
  ogDescription: description
})

const heroTitle = computed(() => {
  const [primary = '', ...secondaryParts] = (page.value?.title ?? '').split('\n')

  return {
    primary,
    secondary: secondaryParts.join(' ').trim()
  }
})

function enterMotion(delay: number = 0) {
  return {
    initial: { opacity: 0, y: 16 },
    animate: { opacity: 1, y: 0 },
    transition: { duration: 0.6, delay }
  }
}

function scrollMotion(delay: number = 0) {
  return {
    initial: { opacity: 0, y: 16 },
    whileInView: { opacity: 1, y: 0 },
    inViewOptions: { once: true, amount: 1 },
    transition: { duration: 0.6, delay }
  }
}

function staggerMotion(index: number = 0) {
  return {
    initial: { opacity: 0 },
    whileInView: { opacity: 1 },
    inViewOptions: { once: true, amount: 1 },
    transition: { duration: 0.6, delay: index * 0.08 }
  }
}

const { copy, copied } = useClipboard()
</script>

<template>
  <div v-if="page">
    <!-- Hero -->
    <UPageHero
      :ui="{
        container: 'relative z-10 max-w-5xl py-6 sm:py-6 lg:py-6 gap-4 sm:gap-y-4',
        wrapper: 'flex flex-col items-center',
        title: 'text-[32px] sm:text-[32px] font-medium tracking-normal leading-tight',
        description: 'mt-4 max-w-md mx-auto text-sm sm:text-sm leading-relaxed text-toned',
        footer: 'mt-6',
        links: 'gap-3'
      }"
    >
      <template #headline>
        <Motion v-bind="enterMotion(0.2)">
          <UBadge
            color="neutral"
            variant="outline"
            :label="page.hero.headline"
            class="rounded-full px-2.5 py-1 gap-1.5 bg-default text-xs font-normal text-toned"
          >
            <template #leading>
              <UChip
                inset
                standalone
                color="success"
                :ui="{ base: 'ring-0' }"
              />
            </template>
          </UBadge>
        </Motion>
      </template>

      <template #title>
        <Motion
          as="span"
          v-bind="enterMotion(0.35)"
          class="inline-block"
        >
          {{ heroTitle.primary }}
          <br v-if="heroTitle.secondary">
          <span
            v-if="heroTitle.secondary"
          >
            {{ heroTitle.secondary }}
          </span>
        </Motion>
      </template>

      <template #description>
        <Motion
          as="span"
          v-bind="enterMotion(0.5)"
          class="inline-block"
        >
          {{ page.description }}
        </Motion>
      </template>

      <template #links>
        <Motion
          class="flex flex-wrap justify-center gap-2"
          v-bind="enterMotion(0.65)"
        >
          <UButton
            v-for="link in page.hero.links"
            :key="link.label"
            v-bind="link"
          />
        </Motion>
      </template>

      <Motion
        as-child
        v-bind="enterMotion(0.85)"
        class="max-w-2xl mx-auto w-full"
      >
        <HeroTerminal :lines="page.terminal.lines" />
      </Motion>

      <Motion
        class="max-w-lg mx-auto w-full"
        v-bind="scrollMotion(0.95)"
      >
        <UPageLogos
          :title="page.logos.title"
          :items="page.logos.items"
          :ui="{
            title: 'text-xs font-normal tracking-normal text-toned',
            logos: 'mt-5 gap-0',
            logo: 'text-toned size-5'
          }"
        />
      </Motion>
    </UPageHero>

    <!-- Features -->
    <UPageSection
      id="features"
      :ui="{
        root: 'border-t border-muted scroll-mt-(--ui-header-height)',
        container: 'max-w-5xl py-12 sm:py-12 lg:py-12 gap-8 sm:gap-8',
        headline: 'font-medium text-xs text-toned tracking-normal text-center',
        title: 'max-w-lg mx-auto text-2xl sm:text-2xl lg:text-2xl font-medium tracking-normal',
        description: 'mt-3 max-w-md mx-auto text-sm sm:text-sm leading-relaxed text-toned'
      }"
    >
      <template #headline>
        <Motion
          as="span"
          v-bind="scrollMotion()"
          class="inline-block"
        >
          {{ page.features.headline }}
        </Motion>
      </template>

      <template #title>
        <Motion
          as="span"
          v-bind="scrollMotion(0.1)"
          class="inline-block"
        >
          {{ page.features.title }}
        </Motion>
      </template>

      <template #description>
        <Motion
          as="span"
          v-bind="scrollMotion(0.2)"
          class="inline-block"
        >
          {{ page.features.description }}
        </Motion>
      </template>

      <div class="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 gap-4">
        <Motion
          v-for="(feature, index) in page.features.items"
          :key="feature.title"
          v-bind="staggerMotion(index)"
        >
          <UPageCard
            :icon="feature.icon"
            :title="feature.title"
            :description="feature.description"
            class="h-full rounded-xl"
            :ui="{
              container: 'p-5 sm:p-5',
              leading: 'mb-3 flex size-8 justify-center rounded-lg bg-muted',
              leadingIcon: 'size-4 text-toned',
              title: 'text-sm font-medium tracking-normal',
              description: 'text-[13px] leading-relaxed text-toned'
            }"
          />
        </Motion>
      </div>
    </UPageSection>

    <!-- Metrics -->
    <UPageSection
      id="metrics"
      :ui="{
        root: 'border-t border-muted scroll-mt-(--ui-header-height)',
        container: 'max-w-5xl py-12 sm:py-12 lg:py-12 gap-8 sm:gap-8',
        headline: 'font-medium text-xs text-toned tracking-normal text-center',
        title: 'max-w-lg mx-auto text-2xl sm:text-2xl lg:text-2xl font-medium tracking-normal',
        description: 'mt-3 max-w-md mx-auto text-sm sm:text-sm leading-relaxed text-toned'
      }"
    >
      <template #headline>
        <Motion
          as="span"
          v-bind="scrollMotion()"
          class="inline-block"
        >
          {{ page.metrics.headline }}
        </Motion>
      </template>

      <template #title>
        <Motion
          as="span"
          v-bind="scrollMotion(0.1)"
          class="inline-block"
        >
          {{ page.metrics.title }}
        </Motion>
      </template>

      <template #description>
        <Motion
          as="span"
          v-bind="scrollMotion(0.2)"
          class="inline-block"
        >
          {{ page.metrics.description }}
        </Motion>
      </template>

      <div class="grid grid-cols-2 lg:grid-cols-4 gap-4">
        <Motion
          v-for="(metric, index) in page.metrics.items"
          :key="metric.label"
          v-bind="staggerMotion(index)"
        >
          <UPageCard
            :title="metric.value"
            :description="metric.label"
            variant="naked"
            :ui="{
              root: 'text-center',
              wrapper: 'items-center',
              title: 'text-2xl font-medium tracking-normal leading-none tabular-nums',
              description: 'text-xs tracking-normal text-toned mt-2'
            }"
          />
        </Motion>
      </div>
    </UPageSection>

    <!-- CTA -->
    <UPageCTA
      variant="naked"
      :ui="{
        root: 'rounded-none border-t border-muted',
        container: 'max-w-3xl mx-auto py-12 sm:py-12 lg:py-12 text-center',
        title: 'text-2xl sm:text-2xl font-medium tracking-normal whitespace-pre-line',
        description: 'mt-3 mx-auto max-w-sm text-sm sm:text-sm leading-relaxed text-toned',
        footer: 'mt-6'
      }"
    >
      <template #title>
        <Motion
          as="span"
          v-bind="scrollMotion()"
          class="inline-block"
        >
          {{ page.cta.title }}
        </Motion>
      </template>

      <template #description>
        <Motion
          as="span"
          v-bind="scrollMotion(0.1)"
          class="inline-block"
        >
          {{ page.cta.description }}
        </Motion>
      </template>

      <template #links>
        <Motion
          class="flex flex-col items-center justify-center gap-3"
          v-bind="scrollMotion(0.2)"
        >
          <UButton
            v-for="link in page.cta.links"
            :key="link.label"
            v-bind="link"
            size="xl"
          />

          <UButton
            :label="page.cta.command"
            :trailing-icon="copied ? 'i-lucide-copy-check' : 'i-lucide-copy'"
            color="neutral"
            variant="outline"
            class="font-mono font-normal text-toned gap-4"
            size="lg"
            :ui="{ trailingIcon: 'size-4' }"
            @click="copy(page.cta.command)"
          />
        </Motion>
      </template>
    </UPageCTA>
  </div>
</template>
