# 11 · File Storage

Stratum stores three kinds of files — user avatars, organization logos, and organization files — through a Supabase S3-compatible client. The storage client is optional: if `STORAGE_URL` isn't set, every storage path is a safe no-op.

## Buckets
Created automatically on API startup:

| Bucket | Holds | Visibility |
|---|---|---|
| `users` | avatars | — |
| `organization` | logos | public |
| `organization-files` | organization files | private |
| `platform` | invoice PDFs | write-only |

## Avatars and logos
Avatars upload to `POST /me/avatar` (≤2 MB, images only) and can be removed. Organization logos upload to `POST /organizations/:id/logo` (admin+). Both are size- and type-checked.

## Organization files
Organization files support folders, search, and a trash — a small file manager. Uploads are limited to 50 MB and count against the organization's storage quota (enforced by BillingGate). Downloads redirect to a signed URL that lives for five minutes.

Folders are stored flat (with a `parent_folder_id`) and the frontend assembles the tree. A folder can only be deleted when it's empty — otherwise a cascade would silently take its subfolders with it.

## The trash

Deleting a file is a **30-day soft-delete**, not an immediate removal. The row is marked deleted and the storage object stays until either an explicit permanent delete or the automatic 30-day sweep. That sweep is deliberately *lazy* — it runs opportunistically when someone lists files or the trash, not on a schedule (Stratum has no cron; see the operations doc). The practical consequence: an organization with no file activity for a long time may keep harmless stale trash rows slightly past 30 days.

Restoring a file returns it to the **root** folder rather than its original location, since that folder may have been moved or deleted in the meantime — the same "restore to a known-safe place" behavior most file managers use.

## Quota accounting
Storage usage sums the sizes of live (non-trashed) files, so trashing a file frees quota immediately rather than waiting for the purge. Like all usage recording, this is fire-and-forget: a failed write is silent and self-corrects on the next change. `POST /billing/usage` exists as a manual correction if drift is ever observed.
