import { CheckIcon, ChevronsUpDownIcon, LogOutIcon, MonitorIcon, MoonIcon, SunIcon } from 'lucide-react'
import { DropdownMenu, DropdownMenuContent, DropdownMenuGroup, DropdownMenuItem, DropdownMenuLabel, DropdownMenuSeparator, DropdownMenuTrigger } from '@/components/ui/dropdown-menu'
import { SidebarMenu, SidebarMenuButton, SidebarMenuItem } from '@/components/ui/sidebar'
import { useTheme, type Theme } from '@/hooks/use-theme'
import { session, signOut } from '@/lib/session'

const themes: { value: Theme; label: string; icon: typeof SunIcon }[] = [
  { value: 'light', label: 'Light', icon: SunIcon },
  { value: 'dark', label: 'Dark', icon: MoonIcon },
  { value: 'system', label: 'System', icon: MonitorIcon },
]

const initials = (name: string) => name.trim().slice(0, 2).toUpperCase() || '?'

export function UserMenu() {
  const { theme, setTheme } = useTheme()
  return (
    <SidebarMenu>
      <SidebarMenuItem>
        <DropdownMenu>
          <DropdownMenuTrigger render={<SidebarMenuButton size="lg" tooltip={session.name} />}>
            <span className="bg-muted text-muted-foreground grid size-8 shrink-0 place-items-center rounded-lg text-xs font-medium">{initials(session.name)}</span>
            <span className="grid flex-1 text-left leading-tight">
              <span className="truncate text-sm font-medium">{session.name}</span>
              <span className="text-muted-foreground truncate text-xs">{session.roles.join(', ') || 'no role'}</span>
            </span>
            <ChevronsUpDownIcon className="ml-auto size-4" />
          </DropdownMenuTrigger>
          <DropdownMenuContent side="top" align="start" className="min-w-56">
            <DropdownMenuGroup>
              <DropdownMenuLabel className="grid leading-tight">
                <span className="text-foreground truncate text-sm font-medium">{session.name}</span>
                <span className="text-muted-foreground truncate text-xs font-normal">Signed in with {session.provider}</span>
              </DropdownMenuLabel>
            </DropdownMenuGroup>
            <DropdownMenuSeparator />
            <DropdownMenuGroup>
              <DropdownMenuLabel>Theme</DropdownMenuLabel>
              {themes.map((t) => (
                <DropdownMenuItem key={t.value} onClick={() => setTheme(t.value)}>
                  <t.icon />
                  {t.label}
                  {theme === t.value && <CheckIcon className="ml-auto" />}
                </DropdownMenuItem>
              ))}
            </DropdownMenuGroup>
            <DropdownMenuSeparator />
            <DropdownMenuItem onClick={() => void signOut()}>
              <LogOutIcon />
              Sign out
            </DropdownMenuItem>
          </DropdownMenuContent>
        </DropdownMenu>
      </SidebarMenuItem>
    </SidebarMenu>
  )
}
