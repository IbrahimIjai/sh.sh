<script setup lang="ts">
import type { NuxtError } from '#app'

const { error } = defineProps<{ error: NuxtError }>()

const notFound = computed(() => error.statusCode === 404)
const { data: user } = useCurrentUser()

useSeoMeta({
  title: () => `${notFound.value ? 'Page not found' : 'Something went wrong'} · ${site.name}`,
  robots: 'noindex, nofollow'
})

function leave(path: string) {
  clearError({ redirect: path })
}
</script>

<template>
  <UApp>
    <div class="flex min-h-dvh flex-col">
      <AppHeader />
      <main class="flex flex-1 items-center justify-center px-5 py-16 sm:px-8">
        <div class="w-full max-w-md text-center">
          <div
            v-if="notFound"
            aria-hidden="true"
            class="flex items-center justify-center gap-2 text-[clamp(88px,22vw,148px)] leading-none font-semibold tracking-[-0.06em] text-highlighted select-none"
          >
            <span>4</span>
            <span class="flex size-[0.78em] items-center justify-center rounded-full border-[0.06em] border-current motion-safe:animate-[wobble_3.2s_ease-in-out_infinite]">
              <UIcon
                name="i-lucide-unlink"
                class="size-[0.38em]"
              />
            </span>
            <span>4</span>
          </div>
          <UIcon
            v-else
            name="i-lucide-cloud-lightning"
            class="size-16 text-highlighted"
          />

          <h1 class="mt-8 text-2xl font-semibold tracking-tight text-highlighted sm:text-3xl">
            {{ notFound ? 'This link took a wrong turn' : 'Something went wrong' }}
          </h1>
          <p class="mx-auto mt-3 max-w-sm text-[15px] leading-relaxed text-toned">
            {{ notFound
              ? 'The page you are looking for does not exist, or it was shortened a little too much.'
              : 'We could not load this page. Give it another try in a moment.' }}
          </p>

          <div class="mt-8 flex flex-col justify-center gap-2 sm:flex-row">
            <UButton
              size="xl"
              class="justify-center"
              @click="leave('/')"
            >
              Take me home
            </UButton>
            <UButton
              v-if="user"
              size="xl"
              variant="outline"
              class="justify-center"
              @click="leave('/dashboard')"
            >
              Go to dashboard
            </UButton>
          </div>
        </div>
      </main>
      <AppFooter />
    </div>
  </UApp>
</template>

<style>
@keyframes wobble {
  0%, 100% { transform: rotate(0deg) translateY(0); }
  25% { transform: rotate(-8deg) translateY(-4%); }
  75% { transform: rotate(8deg) translateY(2%); }
}
</style>
