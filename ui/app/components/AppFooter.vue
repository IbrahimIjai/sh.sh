<script setup lang="ts">
const year = new Date().getFullYear()
</script>

<template>
  <footer class="mx-auto flex w-full max-w-6xl flex-col items-center gap-1 px-5 py-8 text-[13px] text-muted sm:flex-row sm:justify-between sm:px-8">
    <p>© {{ year }} {{ site.name }}</p>
    <p>
      Built by {{ site.author }} ·
      <a
        :href="site.x"
        target="_blank"
        rel="noopener me"
        class="rounded-sm underline-offset-4 hover:text-highlighted hover:underline focus-visible:ring-3 focus-visible:ring-(--ds-ink)/15 focus-visible:outline-none"
      >X</a> ·
      <a
        :href="site.github"
        target="_blank"
        rel="noopener"
        class="rounded-sm underline-offset-4 hover:text-highlighted hover:underline focus-visible:ring-3 focus-visible:ring-(--ds-ink)/15 focus-visible:outline-none"
      >GitHub</a>
    </p>
  </footer>
</template>
