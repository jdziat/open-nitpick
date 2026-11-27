import { Fragment } from 'react'
import { Link, useLocation } from 'react-router'
import { Breadcrumb, BreadcrumbItem, BreadcrumbLink, BreadcrumbList, BreadcrumbPage, BreadcrumbSeparator } from '@/components/ui/breadcrumb'
import { Separator } from '@/components/ui/separator'
import { SidebarTrigger } from '@/components/ui/sidebar'
import { carry } from '@/lib/scope'
import { InsightsControls } from './insights-controls'
import { usePageTitleValue } from './page-title'

const names: Record<string, string> = {
  insights: 'Insights',
  hotspots: 'Hotspots',
  findings: 'Findings',
  reviews: 'Reviews',
  repos: 'Repositories',
  notary: 'Notary',
  records: 'Records',
}

export function SiteHeader() {
  const title = usePageTitleValue()
  const { pathname, search } = useLocation()
  const parts = pathname.split('/').filter(Boolean)
  // Insights pages share a period and repository; the notary has neither.
  const insights = parts[0] === 'insights'

  const crumbs = parts.map((part, i) => {
    const last = i === parts.length - 1
    const label = last && title ? title : (names[part] ?? part)
    return { to: `/${parts.slice(0, i + 1).join('/')}`, label, last }
  })

  return (
    <header className="bg-background/80 sticky top-0 z-20 flex min-h-14 shrink-0 flex-wrap items-center gap-x-2 gap-y-1 border-b px-4 py-2 backdrop-blur">
      <SidebarTrigger className="-ml-1" />
      <Separator orientation="vertical" className="mr-1 data-vertical:h-4 data-vertical:self-center" />
      <Breadcrumb className="min-w-0 flex-1">
        <BreadcrumbList>
          {crumbs.map((c) => (
            <Fragment key={c.to}>
              <BreadcrumbItem className="min-w-0">
                {c.last ? (
                  <BreadcrumbPage className="truncate">{c.label}</BreadcrumbPage>
                ) : (
                  <BreadcrumbLink render={<Link to={{ pathname: c.to, search: carry(search) }} />}>{c.label}</BreadcrumbLink>
                )}
              </BreadcrumbItem>
              {!c.last && <BreadcrumbSeparator />}
            </Fragment>
          ))}
        </BreadcrumbList>
      </Breadcrumb>
      {insights && <InsightsControls />}
    </header>
  )
}
