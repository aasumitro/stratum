import { Link } from "@tanstack/react-router"

const NAV_BASE =
  "flex items-center gap-2.5 rounded-lg px-3 py-2 text-sm transition-colors w-full text-left"
const NAV_ACTIVE = "bg-accent text-accent-foreground font-medium"
const NAV_INACTIVE =
  "text-muted-foreground hover:bg-accent/60 hover:text-accent-foreground"

interface NavLinkProps {
  to: string
  params: Record<string, string>
  icon: React.ReactNode
  label: string
  badge?: React.ReactNode
}

export function NavLink({ to, params, icon, label, badge }: NavLinkProps) {
  return (
    <Link
      to={to as never}
      params={params as never}
      activeProps={{ className: `${NAV_BASE} ${NAV_ACTIVE}` }}
      inactiveProps={{ className: `${NAV_BASE} ${NAV_INACTIVE}` }}
    >
      {icon}
      <span className="flex-1">{label}</span>
      {badge}
    </Link>
  )
}

export function NavSection({ children }: { children: React.ReactNode }) {
  return (
    <p className="px-3 pt-4 pb-1 text-[11px] font-semibold tracking-wider text-muted-foreground/70 uppercase first:pt-0">
      {children}
    </p>
  )
}
