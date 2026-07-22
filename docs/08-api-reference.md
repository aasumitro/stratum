# 08 · API Reference

All endpoints are under `/api/v1`. Requests carry a Supabase JWT (`Authorization: Bearer …`). Responses use a standard envelope.

This page is a hand-maintained overview. For the full, generated request/response schema of every endpoint, see [`specs/`](specs) (`swagger.json`/`.yaml`, regenerated via `make swagger`) or browse it interactively at `/swagger/index.html` against a locally running API (`development` mode only — see `02-getting-started.md`). New handlers must carry a swag doc comment for the generated spec to pick them up.

## Response envelope
```jsonc
{ "data": <T>, "status": { "request_id": "…", "error": false } }          // success
{ "data": [<T>], "status": { … }, "pagination": { … } }                    // paginated
{ "status": { "request_id": "…", "error": true, "code": "…", "message": "…" } }  // error
{ "status": { "code": "VALIDATION_FAILED", "details": { "field": ["…"] } } }     // 422
```

## Account — `/me`
`GET` · `POST` (upsert, required on first login) · `PATCH` (name/avatar) · `PATCH /preferences` · `DELETE` (async 202) · `POST /export` (async 202) · `GET /tasks` · `GET /tasks/:id` · `GET /sessions` · `POST /sessions/revoke-all` · `POST /avatar` · `DELETE /avatar` · `POST /mfa/sync` · `POST /password/changed` (audit marker, empty body) · `GET /audit-log` (filters: `from`/`to`) · `GET /audit-log/export`.

MFA enrolment happens against Supabase directly, never through this API.

## Organizations — `/organizations`
`GET` · `POST` (`plan`/`cycle` required; optional `addons[]`/`coupon_code` cart, validated against the live catalog before the org is written) · `GET /join/preview` (invite code, read-only) · `POST /join` (invite code) · `GET/PATCH/DELETE /:id` · `DELETE /:id/leave` · `POST /:id/transfer` (MFA) · `PATCH /:id/settings` · `POST /:id/suspend` · `POST /:id/unsuspend` (both MFA) · `POST /:id/invite-code` · `PATCH /:id/invite-code` · `POST /:id/logo`.

**Members** `/:id/members`: `GET` · `POST` · `POST /import` (bulk email invite, `dry_run`) · `DELETE /:authSub` · `PATCH /:authSub/role`.

**Invitations** `/:id/invitations`: `GET` · `POST` · `DELETE /:invId`. Plus (no org prefix): `GET /me/invitations`, `GET /invitations/preview?token=`, `POST /invitations/accept`, `POST /invitations/request-new`.

**Audit log** `/:id/audit-log`: `GET` (filters: `actor`/`action`/`resource`/`from`/`to`) · `GET /export` (CSV).

**Webhooks** `/:id/webhooks`: `POST` (Growth+, optional `subscribed_events`) · `GET` (each row has a `health` object) · `PATCH /:wid` · `DELETE /:wid` · `POST /:wid/rotate-secret` · `POST /:wid/test-event` · `GET /:wid/deliveries` (filters) · `POST /:wid/deliveries/:did/retry` · `POST /:wid/deliveries/retry-failed`.

**Files** `/:id/files`: `POST` · `GET` (`folder_id`/`search`/`search_all`) · `GET /trash` · `GET /:fileId/download` · `PATCH /:fileId` (move) · `DELETE /:fileId` (soft) · `POST /bulk-delete` · `POST /:fileId/restore` · `DELETE /:fileId/permanent` · `POST /folders` · `GET /folders` · `PATCH /folders/:folderId` · `DELETE /folders/:folderId`.

## Billing — `/organizations/:id/billing`
Every `GET` is member-visible; every mutation is owner-only.
`GET /` · `PATCH /plan` (MFA) · `POST /cancel` · `POST /resume` · `POST /extend` (MFA) · `POST /activate` (MFA) · `GET /history` · `GET /invoices` · `GET /invoices/:id/pdf?lang=` · `POST /invoices/:id/pay` · `POST /invoices/:id/pay/regenerate` · `GET /payment-links` · `GET /payments` · `GET /usage` · `POST /usage` · `GET /features` · `GET /preview?plan=&cycle=` · `GET /coupons` · `POST /coupons/redeem` · `GET /addons` · `POST /addons` (MFA) · `DELETE /addons/:id` (MFA).

`GET /billing/coupons/eligible` (no organization in the path) — same eligibility check as `GET /coupons`, for the create-organization cart before an org exists.

## Notifications — `/me/notifications`
`GET` (cursor) · `GET /stream` (SSE) · `GET /unread-count` · `PATCH /read-all` (`?organization_id=`) · `PATCH /:id/read` · `GET/PATCH /preferences`.

## References — `/references`
`GET /countries` · `/currencies` · `/plans` · `/features` · `/addons` — all active-only, sorted.

## Webhooks (public, top-level `/webhooks`)
`POST /stripe` · `POST /xendit` (payment providers) · `POST /supabase/user-updated` (email sync). Verified by signature/token/shared-secret; unauthenticated by design.

## Health (no `/api/v1` prefix)
`GET /health` (liveness) · `GET /health/ready` (readiness; 503 = degraded) · `GET /health/stats` (runtime metrics).

## Conventions
Cursor pagination returns a `next_cursor`; page pagination uses `?page=&limit=`. 422 responses carry field-level `details`. Idempotent operations (payment link creation) accept an `Idempotency-Key` header.
