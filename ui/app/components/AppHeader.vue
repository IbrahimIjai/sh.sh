<script setup lang="ts">
import { motion } from 'motion-v'
import type { VariantType } from 'motion-v'

const nuxtApp = useNuxtApp()
const colorMode = useColorMode()
const activeSection = ref<string>()
const isDark = computed(() => colorMode.value === 'dark')
const colorModeLabel = computed(() => isDark.value ? 'Switch to light mode' : 'Switch to dark mode')
const colorModeIcon = computed(() => isDark.value ? 'i-lucide-sun' : 'i-lucide-moon')

function toggleColorMode() {
  const next = isDark.value ? 'light' : 'dark'
  colorMode.preference = next

  if (import.meta.client) {
    document.documentElement.classList.remove('light', 'dark')
    document.documentElement.classList.add(next)
    localStorage.setItem('nuxt-color-mode', next)
  }
}

const items = computed(() => [
  {
    label: 'Features',
    to: '#features',
    exactHash: true,
    active: activeSection.value === 'features'
  },
  {
    label: 'Metrics',
    to: '#metrics',
    exactHash: true,
    active: activeSection.value === 'metrics'
  }
])

nuxtApp.hooks.hookOnce('page:loading:end', () => {
  const observer = new IntersectionObserver((entries) => {
    const visible = entries.find(e => e.isIntersecting)
    if (visible) {
      activeSection.value = visible.target.id
    } else if (entries.every(e => !e.isIntersecting)) {
      activeSection.value = undefined
    }
  }, { rootMargin: '-50% 0px -50% 0px' })

  document.querySelectorAll('#features, #metrics').forEach(el => observer.observe(el))
})

const variants: Record<string, VariantType | ((custom: unknown) => VariantType)> = {
  normal: {
    rotate: 0,
    y: 0,
    opacity: 1
  },
  close: (custom: unknown) => {
    const c = custom as number
    return {
      rotate: c === 1 ? 45 : c === 3 ? -45 : 0,
      y: c === 1 ? 6 : c === 3 ? -6 : 0,
      opacity: c === 2 ? 0 : 1,
      transition: {
        type: 'spring',
        stiffness: 260,
        damping: 20
      }
    }
  }
}
</script>

<template>
  <div class="relative">
    <UHeader>
      <template #left>
        <NuxtLink
          to="/"
          class="focus-visible:outline-3 outline-primary/25 rounded-md p-1 -ms-1"
        >
          <AppLogo class="h-6 w-auto shrink-0" />
        </NuxtLink>

        <TemplateMenu />
      </template>

      <UNavigationMenu
        :items="items"
        variant="link"
      />

      <template #right>
        <UButton
          label="Sign in"
          color="neutral"
          variant="ghost"
          class="hidden lg:flex"
        />
        <UButton
          label="Get started"
          color="neutral"
          class="hidden lg:flex"
          to="https://ui.nuxt.com"
          target="_blank"
        />
      </template>

      <template #toggle="{ open, toggle, ui }">
        <div class="flex items-center gap-1 lg:hidden">
          <UButton
            size="sm"
            variant="ghost"
            color="neutral"
            square
            :aria-label="open ? 'Close navigation' : 'Open navigation'"
            :aria-expanded="open"
            :class="ui.toggle({ toggleSide: 'right' })"
            @click="toggle"
          >
            <svg
              xmlns="http://www.w3.org/2000/svg"
              class="size-5"
              viewBox="0 0 24 24"
              fill="none"
              stroke="currentColor"
              stroke-width="2"
              stroke-linecap="round"
              stroke-linejoin="round"
            >
              <motion.line
                x1="4"
                y1="6"
                x2="20"
                y2="6"
                :variants="variants"
                :animate="open ? 'close' : 'normal'"
                :custom="1"
                tabindex="-1"
              />
              <motion.line
                x1="4"
                y1="12"
                x2="20"
                y2="12"
                :variants="variants"
                :animate="open ? 'close' : 'normal'"
                :custom="2"
                tabindex="-1"
              />
              <motion.line
                x1="4"
                y1="18"
                x2="20"
                y2="18"
                :variants="variants"
                :animate="open ? 'close' : 'normal'"
                :custom="3"
                tabindex="-1"
              />
            </svg>
          </UButton>
        </div>
      </template>

      <template #body>
        <UNavigationMenu
          :items="items"
          orientation="vertical"
        />

        <div class="mt-4 flex flex-col gap-2">
          <UButton
            label="Sign in"
            color="neutral"
            variant="soft"
            block
          />
          <UButton
            label="Get started"
            block
            to="https://ui.nuxt.com"
            target="_blank"
          />
        </div>
      </template>
    </UHeader>

    <div class="pointer-events-none absolute inset-x-0 top-0 z-[60] mx-auto flex h-14 max-w-6xl items-center justify-end px-4 sm:px-6 lg:px-8">
      <button
        type="button"
        class="pointer-events-auto me-11 inline-flex size-8 items-center justify-center rounded-lg text-toned transition-colors hover:bg-elevated focus-visible:outline-3 focus-visible:outline-primary/25 lg:me-40"
        :aria-label="colorModeLabel"
        @click="toggleColorMode"
      >
        <UIcon
          :name="colorModeIcon"
          class="size-4"
        />
      </button>
    </div>
  </div>
</template>
