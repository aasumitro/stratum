# 10 · Web UI

The web app (`ui/app`) is the primary tenant-facing product surface — everything an organization's members do day to day. It's a single client of the API contract; it never invents endpoints or types.

## Stack
React 19 with strict TypeScript, built by Vite. TanStack Router (file-based), Query, and Form handle routing, server state, and forms; Axios is the HTTP client; Tailwind CSS v4 and shadcn/ui provide styling and components; Supabase's client SDK handles auth; react-i18next covers EN/ID. State discipline matters: **server data always goes through TanStack Query** — there is no Redux/Zustand store for it.

## How data flows
All calls to our backend go through thin wrappers in `lib/api/*` (`useHTTPQuery`, `useHTTPActionPost/Patch/…`, `useHTTPActionUpload`). This isn't just style — the wrapper's error handling and response-unwrapping are load-bearing, and bypassing it has caused real bugs. The exceptions that legitimately stay raw are the Supabase SDK, the SSE notification stream, and binary downloads. Auth works by copying the Supabase access token into a `__session` cookie that an Axios interceptor attaches.

## Structure
```
src/
├── components/  layout (one shell: sidebar + top bar everywhere) · shared primitives · ui (shadcn, generated)
├── features/    auth · onboarding · organization · billing · notification · account   (each: hooks + components + pages)
├── hooks/       cross-feature hooks (active org, permissions, billing status, …)
├── lib/         api/* · auth · i18n · router
├── routes/      file-based tree (router.gen.ts is the ACTIVE generated tree)
└── types/       every server shape — use them, never invent
```

Route files export only a `Route`; pages live in `features/*/pages/`. A set of shared primitives (`DataTable`, `FilterBar`, `ConfirmationDialog`, `SideDrawer`, `StatusBadge`, `Banner`, `PermissionGuard`, …) means new pages compose rather than hand-roll.

## What's built
The full product: authentication and an onboarding wizard; organization management (members and invitations, settings, audit log, webhooks); a single-page billing surface (subscription, usage, invoices with inline payment-attempt history, add-ons, plan history/features as on-demand panels) where every mutation — cancel, upgrade, downgrade, extend, activate — is a guided multi-step wizard showing the real price before you confirm, not a bare "are you sure?" dialog; account (profile, preferences, security with TOTP MFA, personal audit log); and a notification center with a preferences matrix. It's fully translated EN/ID, has light/dark/system theming, keyboard shortcuts, a responsive sidebar, and PostHog analytics.

## Conventions
Loading states use skeletons that mirror the real layout; empty and error states use shared components. RBAC on the frontend only hides or locks controls — the backend enforces real permissions. When a route is added, the generated `router.gen.ts` needs updates in several places (a checklist lives with the file). All user-facing text goes through i18n with keys in both `en.json` and `id.json`.

## A few backend realities to keep in mind
Billing cancel/resume are `POST` (not `PATCH`); the change-plan body needs both `plan` and `cycle`; a soft-deleted organization returns 403 (not 404); MFA is TOTP-only and handled by Supabase directly; and email changes are a dual-confirmation Supabase flow. See `08-api-reference.md` and `05-authentication.md`.
