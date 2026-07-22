import type { ReactNode } from "react"
import { BrandPanel } from "@/components/layout/brand-panel"

export function AuthLayout({ children }: { children: ReactNode }) {
  return (
    <div className="flex min-h-svh">
      <BrandPanel className="justify-between lg:w-2/5">
        <div className="relative z-10">
          <span className="text-xl font-extrabold tracking-widest text-white/90 uppercase">
            Stratum
          </span>
        </div>

        <div className="relative z-10">
          <p className="mb-1 text-xs font-medium tracking-widest text-white/50 uppercase">
            Infrastructure for builders
          </p>
          <h2 className="text-3xl leading-tight font-extrabold text-white">
            Everything except
            <br />
            the product.
          </h2>
        </div>
      </BrandPanel>

      <div className="flex flex-1 items-center justify-center bg-background p-6 lg:p-16">
        <div className="w-full max-w-sm">{children}</div>
      </div>
    </div>
  )
}
