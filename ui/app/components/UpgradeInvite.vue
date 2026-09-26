<template>
  <section
    aria-labelledby="upgrade-title"
    class="mx-auto w-full max-w-3xl px-5 sm:px-8"
  >
    <div class="flex flex-col items-center gap-5 border-t border-default py-10 text-center sm:flex-row sm:items-end sm:justify-between sm:gap-10 sm:text-left">
      <div class="max-w-md">
        <h2
          id="upgrade-title"
          class="text-[15px] font-medium text-highlighted"
        >
          Want to see what happens after the click?
        </h2>
        <p class="mt-1.5 text-sm leading-relaxed text-muted">
          Upgrade to see who visited your links, where they came from, and how they perform.
        </p>
        <p class="mt-3 text-sm font-semibold text-highlighted">
          {{ site.price }}
        </p>
      </div>
      <UButton
        :to="site.upgradePath"
        size="lg"
        class="shrink-0"
      >
        Upgrade
      </UButton>
    </div>
  </section>
</template>
