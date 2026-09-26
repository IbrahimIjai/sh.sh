<script setup lang="ts">
import type { DropdownMenuItem } from '@nuxt/ui'

const route = useRoute()
const { data: user } = useCurrentUser()
const logout = useLogout()

const accountItems = computed<DropdownMenuItem[][]>(() => [
  [{ type: 'label', label: user.value?.email }],
  [{ label: 'Log out', icon: 'i-lucide-log-out', onSelect: () => logout.mutate() }]
])
</script>

<template>
  <header class="mx-auto flex h-14 w-full max-w-6xl items-center justify-between px-5 sm:px-8">
    <NuxtLink
      to="/"
      class="-mx-1 rounded-md px-1 text-[15px] font-semibold tracking-tight text-highlighted outline-none focus-visible:ring-3 focus-visible:ring-(--ds-ink)/15"
    >
      {{ site.name }}
    </NuxtLink>

    <div class="flex items-center gap-2">
      <ClientOnly>
        <UColorModeButton
          color="neutral"
          variant="ghost"
        />
        <template #fallback>
          <div class="size-7" />
        </template>
      </ClientOnly>

      <UDropdownMenu
        v-if="user"
        :items="accountItems"
        :content="{ align: 'end' }"
      >
        <UButton
          color="neutral"
          variant="ghost"
          size="sm"
          icon="i-lucide-circle-user"
          :label="user.email"
          :aria-label="`Account menu for ${user.email}`"
          :ui="{ label: 'hidden max-w-44 truncate sm:inline' }"
        />
      </UDropdownMenu>
      <UButton
        v-else-if="route.path !== '/login'"
        to="/login"
        color="neutral"
        variant="outline"
        size="sm"
      >
        Log in
      </UButton>
    </div>
  </header>
</template>
