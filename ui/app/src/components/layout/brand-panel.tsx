import type { ReactNode } from "react"
import stonesImage from "@/assets/stones.jpg"
import { cn } from "@/lib/ui"

const STRATA = [
  { width: "82%", delay: "0s" },
  { width: "55%", delay: "0.5s" },
  { width: "91%", delay: "1.1s" },
  { width: "43%", delay: "0.3s" },
  { width: "73%", delay: "0.8s" },
  { width: "88%", delay: "1.4s" },
  { width: "61%", delay: "0.2s" },
]

/**
 * Dark, photo-backed hero panel shared by the auth screens and the
 * onboarding wizard — same background image, vignette, and animated strata
 * bars everywhere it appears, so those surfaces read as one brand moment.
 */
export function BrandPanel({
  children,
  className,
}: {
  children: ReactNode
  className?: string
}) {
  return (
    <div
      className={cn(
        // Fixed dark colors, not the theme's primary/primary-foreground —
        // this panel is always a dark photo backdrop, so its text must stay
        // light even when the rest of the app is in light mode (where
        // primary/primary-foreground would otherwise flip).
        "relative hidden flex-col overflow-hidden bg-neutral-950 p-10 text-white lg:flex",
        className
      )}
    >
      <div
        className="absolute inset-0"
        style={{
          backgroundImage: `url(${stonesImage})`,
          backgroundSize: "160%",
          backgroundPosition: "center",
          backgroundRepeat: "no-repeat",
        }}
        aria-hidden
      />
      {/* Vignette — darkens toward the edges so the center (where the
          strata/logo/copy sit) stays the focal point and text keeps
          contrast regardless of what's behind it in the source photo. */}
      <div
        className="absolute inset-0"
        style={{
          background:
            "radial-gradient(circle at center, rgba(0,0,0,0.4) 0%, rgba(0,0,0,0.7) 55%, rgba(0,0,0,0.94) 100%)",
        }}
        aria-hidden
      />

      {children}

      <div
        className="pointer-events-none absolute right-0 bottom-0 left-0 flex flex-col gap-2.5 px-10 pb-10"
        aria-hidden
      >
        {STRATA.map((s, i) => (
          <div
            key={i}
            className="h-1.5 animate-strata rounded-full bg-white/20"
            style={{ width: s.width, animationDelay: s.delay }}
          />
        ))}
      </div>
    </div>
  )
}
