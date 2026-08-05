# 11 · File Storage

Stratum stores two kinds of files — user avatars and organization logos — through a Supabase S3-compatible client. The storage client is optional: if `STORAGE_URL` isn't set, every storage path is a safe no-op.

## Buckets
Created automatically on API startup:

| Bucket | Holds | Visibility |
|---|---|---|
| `users` | avatars | — |
| `organization` | logos | public |
| `platform` | invoice PDFs | write-only |

## Avatars and logos
Avatars upload to `POST /me/avatar` (≤2 MB, images only) and can be removed. Organization logos upload to `POST /organizations/:id/logo` (admin+). Both are size- and type-checked.
