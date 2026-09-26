# Nuxt Landing Template

[![Nuxt UI](https://img.shields.io/badge/Made%20with-Nuxt%20UI-00DC82?logo=nuxt&labelColor=020420)](https://ui.nuxt.com)

Use this template to build your own landing page with [Nuxt UI](https://ui.nuxt.com) quickly.

- [Live demo](https://landing-template.nuxt.dev/)
- [Documentation](https://ui.nuxt.com/docs/getting-started/installation/nuxt)

<a href="https://landing-template.nuxt.dev/" target="_blank">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="https://ui.nuxt.com/assets/templates/nuxt/landing-dark.png">
    <source media="(prefers-color-scheme: light)" srcset="https://ui.nuxt.com/assets/templates/nuxt/landing-light.png">
    <img alt="Nuxt Landing Template" src="https://ui.nuxt.com/assets/templates/nuxt/landing-light.png">
  </picture>
</a>

## Quick Start

```bash [Terminal]
npm create nuxt@latest -- -t ui/landing
```

## Deploy your own

[![Deploy with Vercel](https://vercel.com/button)](https://vercel.com/new/clone?repository-name=landing&repository-url=https%3A%2F%2Fgithub.com%2Fnuxt-ui-templates%2Flanding&demo-image=https%3A%2F%2Fui.nuxt.com%2Fassets%2Ftemplates%2Fnuxt%2Flanding-dark.png&demo-url=https%3A%2F%2Flanding-template.nuxt.dev%2F&demo-title=Nuxt%20Landing%20Template&demo-description=A%20modern%20landing%20page%20template%20powered%20by%20Nuxt%20Content.)

## UI Theme

The UI follows the SMRY-inspired brief in `app/assets/css/main.css` and
`app/app.config.ts`: Paper (`#F7F4EB`) for the page, white component surfaces,
Ink (`#111111`) for text and primary actions, and Geist loaded by Nuxt Fonts.
Dark mode is the default preference, with a header toggle for switching to light;
the preference is retained by Nuxt Color Mode.

Typography stays compact: 12px labels, 13px supporting text, 14px buttons and
body copy, 24px section headings, and a 32px hero. These are this project's
compact adaptation, not a claim that every size exactly matches SMRY.
The live SMRY interface was checked on 26 September 2026: it uses Geist,
mostly 14px controls, and 12-13px supporting labels; its large hero uses Literata.

Keep `--ui-bg` white in light mode and charcoal in dark mode; Paper belongs on
the page canvas in light mode and becomes a warm charcoal canvas in dark mode.
Radius utilities are
explicitly set to retain 8px controls, 12px cards, and 16px floating panels
because Nuxt UI otherwise multiplies them by `--ui-radius`. Use semantic
success/error/warning colors for feedback and preserve keyboard focus rings.

## Setup

Make sure to install the dependencies:

```bash
bun install
```

## Development Server

Start the development server on `http://localhost:3000`:

```bash
bun run dev
```

## Production

Build the application for production:

```bash
bun run build
```

Locally preview production build:

```bash
bun run preview
```

Check out the [deployment documentation](https://nuxt.com/docs/getting-started/deployment) for more information.

## Renovate integration

Install [Renovate GitHub app](https://github.com/apps/renovate/installations/select_target) on your repository and you are good to go.
