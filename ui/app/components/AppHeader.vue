<template>
  <header class="mx-auto flex h-14 w-full max-w-6xl items-center justify-between px-5 sm:px-8">
    <NuxtLink
      to="/"
      class="-mx-1 rounded-md px-1 text-[15px] font-semibold tracking-tight text-highlighted outline-none focus-visible:ring-3 focus-visible:ring-(--ds-ink)/15"
    >
      {{ site.name }}
    </NuxtLink>
    <ClientOnly>
      <UColorModeButton
        color="neutral"
        variant="ghost"
      />
      <template #fallback>
        <div class="size-7" />
      </template>
    </ClientOnly>
  </header>
</template>
