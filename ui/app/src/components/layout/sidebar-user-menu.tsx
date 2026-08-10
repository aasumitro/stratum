import { IconSelector } from "@tabler/icons-react"
import { SidebarMenu, SidebarMenuItem } from "@/components/ui/sidebar"
import { Avatar, AvatarFallback, AvatarImage } from "@/components/ui/avatar"
import { UserMenu } from "@/components/layout/user-menu"
import { useUserDisplay } from "@/hooks/use-user-display"
import { initials } from "@/lib/format"

export function SidebarUserMenu() {
  const { profile, email } = useUserDisplay()
  const userInitials = initials(profile?.full_name || email || "U")

  return (
    <SidebarMenu>
      <SidebarMenuItem>
        <UserMenu
          side="top"
          align="start"
          triggerClassName="flex w-full items-center gap-2 rounded-xl px-3 py-2 text-sm text-sidebar-foreground transition-colors group-data-[collapsible=icon]:justify-center group-data-[collapsible=icon]:px-2 hover:bg-sidebar-accent hover:text-sidebar-accent-foreground"
        >
          <Avatar className="size-7 shrink-0 rounded-lg">
            <AvatarImage src={profile?.avatar_url || undefined} alt="" />
            <AvatarFallback className="rounded-lg bg-sidebar-accent text-xs text-sidebar-accent-foreground">
              {userInitials}
            </AvatarFallback>
          </Avatar>
          <div className="flex min-w-0 flex-1 flex-col text-left group-data-[collapsible=icon]:hidden">
            <span className="truncate text-xs font-semibold">
              {profile?.full_name ?? email}
            </span>
            <span className="truncate text-xs text-sidebar-foreground/50">
              {email}
            </span>
          </div>
          <IconSelector className="ml-auto size-4 shrink-0 text-sidebar-foreground/50 group-data-[collapsible=icon]:hidden" />
        </UserMenu>
      </SidebarMenuItem>
    </SidebarMenu>
  )
}
