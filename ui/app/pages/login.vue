<script setup lang="ts">
import { toast } from 'vue-sonner'

const EMAIL_PATTERN = /^[^\s@]+@[^\s@]+\.[^\s@]+$/

const route = useRoute()
const { data: user } = useCurrentUser()
const requestLink = useRequestMagicLink()

const email = ref('')
const error = ref('')
const sentTo = ref('')
const input = useTemplateRef('input')

const linkFailed = computed(() => typeof route.query.error === 'string')

useSeoMeta({
  title: `Log in · ${site.name}`,
  description: `Log in to ${site.name} with a link sent to your email.`,
  robots: 'noindex, nofollow'
})

watch(user, (current) => {
  if (current) navigateTo('/')
}, { immediate: true })

function showInline(message: string) {
  error.value = message
  input.value?.focus()
}

function submit() {
  if (requestLink.isPending.value) return

  const value = email.value.trim()
  if (!value) return showInline('Enter your email address.')
  if (!EMAIL_PATTERN.test(value)) return showInline('That does not look like an email address.')

  error.value = ''
  requestLink.mutate(value, {
    onSuccess: () => {
      sentTo.value = value
      toast.success('Link sent. Check your inbox.')
    },
    onError: (err) => {
      const status = apiStatus(err)
      if (status === 400) return showInline('That does not look like an email address.')
      if (status === 429) toast.error('Too many attempts. Wait a minute and try again.')
      else toast.error('The link could not be sent. Try again.')
    }
  })
}

async function changeEmail() {
  sentTo.value = ''
  await nextTick()
  input.value?.focus()
}

watch(email, () => {
  error.value = ''
})
</script>

<template>
  <section class="mx-auto w-full max-w-sm px-5 pt-[10vh] sm:pt-[16vh]">
    <div v-if="!sentTo">
      <h1 class="text-3xl font-semibold tracking-tight text-highlighted">
        Log in
      </h1>
      <p class="mt-2 text-[15px] leading-relaxed text-toned">
        We will email you a link to log in. No password needed.
      </p>

      <p
        v-if="linkFailed"
        role="alert"
        class="mt-6 rounded-lg border border-default bg-muted px-3 py-2.5 text-[13px] text-toned"
      >
        That link has expired or was already used. Send yourself a new one.
      </p>

      <form
        class="mt-6"
        novalidate
        @submit.prevent="submit"
      >
        <label
          for="email"
          class="text-[13px] font-medium text-toned"
        >Email</label>
        <input
          id="email"
          ref="input"
          v-model="email"
          type="email"
          autocomplete="email"
          autocapitalize="off"
          spellcheck="false"
          placeholder="you@example.com"
          :readonly="requestLink.isPending.value"
          :aria-invalid="error ? 'true' : undefined"
          :aria-describedby="error ? 'email-error' : undefined"
          class="mt-1.5 h-12 w-full rounded-lg border border-default bg-default px-3 text-base text-highlighted shadow-(--ds-shadow-xs) outline-none transition-[border-color,box-shadow] duration-150 placeholder:text-dimmed focus-visible:border-accented focus-visible:ring-3 focus-visible:ring-(--ds-ink)/8"
        >
        <p
          v-if="error"
          id="email-error"
          aria-live="polite"
          class="mt-2 text-[13px] text-error"
        >
          {{ error }}
        </p>
        <UButton
          type="submit"
          size="xl"
          block
          :loading="requestLink.isPending.value"
          :disabled="requestLink.isPending.value"
          class="mt-4 h-12 justify-center"
        >
          Send link
        </UButton>
      </form>
    </div>

    <div v-else>
      <h1 class="text-3xl font-semibold tracking-tight text-highlighted">
        Check your inbox
      </h1>
      <p class="mt-2 text-[15px] leading-relaxed text-toned">
        We sent a link to <span class="font-medium text-highlighted">{{ sentTo }}</span>.
        Open it on this device to log in here.
      </p>
      <UButton
        variant="link"
        size="sm"
        class="mt-4 px-0 text-[13px] text-toned hover:text-highlighted"
        @click="changeEmail"
      >
        Use a different email
      </UButton>
    </div>
  </section>
</template>
