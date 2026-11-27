import { BookMarkedIcon, DatabaseIcon, FlameIcon, FolderGitIcon, GitPullRequestArrowIcon, LayoutDashboardIcon, ListChecksIcon } from 'lucide-react'
import { matchPath, NavLink, useLocation } from 'react-router'
import {
  Sidebar,
  SidebarContent,
  SidebarFooter,
  SidebarGroup,
  SidebarGroupContent,
  SidebarGroupLabel,
  SidebarHeader,
  SidebarMenu,
  SidebarMenuButton,
  SidebarMenuItem,
  SidebarRail,
} from '@/components/ui/sidebar'
import { hasComponent, hasRole } from '@/lib/session'
import { UserMenu } from './user-menu'

type Item = { to: string; label: string; icon: typeof FlameIcon; end?: boolean }

const insights: Item[] = [
  { to: '/insights', label: 'Overview', icon: LayoutDashboardIcon, end: true },
  { to: '/insights/hotspots', label: 'Hotspots', icon: FlameIcon },
  { to: '/insights/findings', label: 'Findings', icon: ListChecksIcon },
  { to: '/insights/reviews', label: 'Reviews', icon: GitPullRequestArrowIcon },
  { to: '/insights/repos', label: 'Repositories', icon: FolderGitIcon },
]
const notary: Item[] = [{ to: '/notary', label: 'Records', icon: DatabaseIcon, end: false }]

// The period and repository the reader chose follow them from page to page.
const KEEP = ['days', 'repo']

export function AppSidebar() {
  const { search } = useLocation()
  const keep = new URLSearchParams(search)
  const carried = new URLSearchParams()
  for (const k of KEEP) if (keep.get(k)) carried.set(k, keep.get(k)!)
  const suffix = carried.toString() ? `?${carried}` : ''

  const groups = [
    hasComponent('insights') && hasRole('insights') ? { label: 'Insights', items: insights, carry: true } : null,
    hasComponent('notary') && hasRole('authoritative') ? { label: 'Notary', items: notary, carry: false } : null,
  ].filter((g) => g !== null)

  return (
    <Sidebar collapsible="icon">
      <SidebarHeader>
        <SidebarMenu>
          <SidebarMenuItem>
            <SidebarMenuButton size="lg" render={<NavLink to="/" />} tooltip="nitpick">
              <span className="bg-brand text-brand-foreground grid size-8 shrink-0 place-items-center rounded-lg">
                <BookMarkedIcon className="size-4" />
              </span>
              <span className="grid flex-1 text-left leading-tight">
                <span className="truncate text-sm font-semibold tracking-tight">nitpick</span>
                <span className="text-muted-foreground truncate text-xs">Review hub</span>
              </span>
            </SidebarMenuButton>
          </SidebarMenuItem>
        </SidebarMenu>
      </SidebarHeader>
      <SidebarContent>
        {groups.map((g) => (
          <SidebarGroup key={g.label}>
            <SidebarGroupLabel>{g.label}</SidebarGroupLabel>
            <SidebarGroupContent>
              <SidebarMenu>
                {g.items.map((item) => (
                  <SidebarMenuItem key={item.to}>
                    <NavItem item={item} suffix={g.carry ? suffix : ''} />
                  </SidebarMenuItem>
                ))}
              </SidebarMenu>
            </SidebarGroupContent>
          </SidebarGroup>
        ))}
        {groups.length === 0 && (
          <p className="text-muted-foreground px-4 py-3 text-sm group-data-[collapsible=icon]:hidden">
            This account holds no role that opens a page.
          </p>
        )}
      </SidebarContent>
      <SidebarFooter>
        <UserMenu />
      </SidebarFooter>
      <SidebarRail />
    </Sidebar>
  )
}

function NavItem({ item, suffix }: { item: Item; suffix: string }) {
  const { pathname } = useLocation()
  const active = matchPath({ path: item.to, end: item.end ?? false }, pathname) !== null
  return (
    <SidebarMenuButton isActive={active} tooltip={item.label} render={<NavLink to={`${item.to}${suffix}`} end={item.end} />}>
      <item.icon />
      <span>{item.label}</span>
    </SidebarMenuButton>
  )
}
