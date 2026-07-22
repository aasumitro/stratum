import { type ClassValue, clsx } from "clsx"
import { twMerge } from "tailwind-merge"

export function cn(...inputs: ClassValue[]) {
  return twMerge(clsx(inputs))
}

// Wails errors are {message, cause, kind} shaped, but the actual text can
// show up as: err itself being a JSON string of that shape, OR err being an
// object/Error whose .message field is itself that JSON string (observed
// from CatalogService rejections — the Go error gets wrapped once, then the
// whole wrapper gets stringified into .message again). Unwrap in two passes:
// first pull out whatever looks like the message text, then if THAT text is
// still JSON-shaped, parse once more and pull the inner message.
function unwrapJSONMessage(text: string): string {
  try {
    const parsed = JSON.parse(text)
    if (parsed && typeof parsed === "object" && "message" in parsed) {
      return String((parsed as { message: unknown }).message)
    }
  } catch {
    // not JSON — text is already the real message
  }
  return text
}

export function wailsError(err: unknown): string {
  let text: string
  if (typeof err === "string") {
    text = err
  } else if (err && typeof err === "object" && "message" in err) {
    text = String((err as { message: unknown }).message)
  } else {
    text = String(err)
  }
  return unwrapJSONMessage(text)
}
