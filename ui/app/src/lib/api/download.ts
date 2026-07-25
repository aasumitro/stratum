import { api } from "./axios"

export async function downloadFile(
  url: string,
  filename: string,
  responseType: "blob" | "arraybuffer" = "blob"
): Promise<void> {
  // No timeout — the shared `api` instance's 5s default is sized for JSON
  // requests, not a large binary export/download.
  const res = await api.get(url, { responseType, timeout: 0 })
  const blob =
    responseType === "arraybuffer"
      ? new Blob([res.data as ArrayBuffer])
      : (res.data as Blob)
  const blobUrl = URL.createObjectURL(blob)
  const a = document.createElement("a")
  a.href = blobUrl
  a.download = filename
  document.body.appendChild(a)
  a.click()
  document.body.removeChild(a)
  setTimeout(() => URL.revokeObjectURL(blobUrl), 1000)
}

// Fetches an authenticated binary response as a blob: URL, for opening in a
// browser-native viewer (e.g. window.open) instead of forcing a save —
// authenticated endpoints can't just be window.open()'d directly like a
// public URL, they need the bearer token the shared `api` instance attaches.
export async function fetchBlobUrl(
  url: string,
  mimeType: string
): Promise<string> {
  const res = await api.get(url, { responseType: "arraybuffer", timeout: 0 })
  const blob = new Blob([res.data as ArrayBuffer], { type: mimeType })
  return URL.createObjectURL(blob)
}
