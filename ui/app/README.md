# Stratum — Web UI

Authenticated web app for Stratum: organization management, billing, notifications, files, webhooks, profile, and account settings. The API is the source of truth — this app never invents endpoints or types.

## Documentation

Full documentation lives with the rest of the project docs:

- **[`docs/10-web-ui.md`](../../docs/10-web-ui.md)** — structure, routing, state, features, and conventions.

See also [`docs/`](../../docs/) — `08-api-reference`, `05-authentication`, and `04-billing` are the most relevant when building web features.

## Stack
React 19 · TypeScript (strict) · Vite 8 · TanStack Router/Query/Form v1 · Axios · Tailwind CSS v4 · shadcn/ui (style `base-rhea`, color `mist`) · Tabler Icons · Supabase Auth · react-i18next (EN + ID) · Figtree Variable.

## Setup

```bash
cp .env.example .env
# Fill: VITE_SERVER_URL, VITE_SUPABASE_URL, VITE_SUPABASE_ANON_KEY
npm install
npm run dev        # http://localhost:3000
npm run build      # tsc -b && vite build
npm run typecheck  # tsc --noEmit
npm run lint       # eslint
npm run format     # prettier --write
```

## Environment

```env
VITE_SERVER_URL=http://localhost:8000/api/v1
VITE_SUPABASE_URL=https://xxx.supabase.co
VITE_SUPABASE_ANON_KEY=eyJ...
```

> `src/router.gen.ts` is the active route tree — `routeTree.gen.ts` is stale, do not use. All backend calls go through the `lib/api/*` wrappers; all UI text goes through i18n (EN + ID). See the docs above for the full route tree, feature list, and conventions.
