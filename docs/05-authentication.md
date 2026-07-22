# 05 · Authentication & Authorization

Stratum never implements its own authentication. Identity is delegated to Supabase Auth; Stratum validates the tokens they issue and layers its own authorization on top.

## Authentication

Users sign in against Supabase and receive a JWT. Every API request carries that token, and the auth middleware validates it against the provider's published **JWKS** (JSON Web Key Set) — Stratum holds no shared secret. Validation pins the accepted signing algorithms (`RS256/384/512`, `ES256/384/512`) to prevent algorithm-confusion attacks, and checks the issuer and audience. The verified claims are injected into the request context; the `sub` claim becomes `auth_sub`, the only identity reference Stratum stores.

**Revocation.** Each token is checked against a Redis blocklist on every request, keyed by `SessionIdentifier(claims)` — Supabase's `session_id` claim, checked first, with `jti` only as a fallback for other JWKS-publishing IdPs. This matters: real Supabase access tokens carry no `jti` claim at all, so a `jti`-only check would silently never match. Signing out everywhere (`POST /me/sessions/revoke-all`) invalidates Supabase's refresh tokens via the Admin API and adds the current session's identifier to the blocklist with a TTL matching the token's remaining life.

## MFA

Multi-factor auth is TOTP-only and handled entirely client-side against Supabase — the backend never touches factors. `POST /me/mfa/sync` re-checks the user's factors via the Admin API and stores an authoritative `mfa_enabled` flag (it never trusts a client-supplied value). Sensitive routes — ownership transfer, plan change, add-on changes, account/organization deletion — require an MFA-verified session, but only for users who have actually enrolled MFA.

Email and password changes work the same way: they happen client-side against Supabase, and Stratum only reacts (a Supabase Database Webhook syncs the new email; a small audit-marker endpoint records the password change).

## Authorization

Once a user is authenticated, three layers decide what they can do:

- **Organization scoping** — the organization middleware resolves the active tenant and rejects non-active ones. (A soft-deleted organization returns 403, not 404, because the record is found but not active.)
- **RBAC** — a fixed three-tier model, `owner > admin > member`. Role-gated routes are enforced by middleware, with role lookups cached in Redis for 30 seconds. On the frontend, RBAC is UI-only (hiding or locking controls); the backend is always the real gate.
- **Row-level security** — enabled on the billing schema at the database level.

A few authorization rules worth internalizing: every billing `GET` is visible to any member while every mutation is owner-only; self-service unsuspend only reverses a self-service suspension (a billing hold can't be waved away); and saving an IP allowlist that would lock out the caller is rejected outright.

## Audit

Authorization events are observable: every mutation — and every failed request, including anonymous 401/403s recorded as `actor="anonymous"` — is written to an append-only audit log automatically by middleware, with sensitive fields (passwords, tokens, secrets) redacted from the stored copy while the handler still receives the real body.
