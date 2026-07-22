export function setCookie(
  name: string,
  value: string,
  days?: number,
  path: string = "/"
): void {
  let expires = ""
  if (days) {
    const date = new Date()
    date.setTime(date.getTime() + days * 24 * 60 * 60 * 1000)
    expires = `; expires=${date.toUTCString()}`
  }
  // Secure requires HTTPS — skip it in dev (typically http://localhost) so
  // the cookie still gets set; SameSite=Strict applies in both.
  const secure = import.meta.env.PROD ? "; Secure" : ""
  document.cookie = `${name}=${encodeURIComponent(value || "")}${expires}; path=${path}; SameSite=Strict${secure}`
}

export function getCookie(name: string): string | null {
  const value = `; ${document.cookie}`
  const parts = value.split(`; ${name}=`)
  if (parts.length === 2) return parts.pop()!.split(";").shift() || null
  return null
}

export function deleteCookie(name: string, path: string = "/"): void {
  document.cookie = `${name}=; expires=Thu, 01 Jan 1970 00:00:00 UTC; path=${path}`
}
