import { ArrowRightIcon, TerminalIcon } from 'lucide-react'
import { Link, useLocation } from 'react-router'
import { Bars } from '@/components/bars'
import { DailyChart } from '@/components/daily-chart'
import { DataTable } from '@/components/data-table/data-table'
import { hotspotColumns, reviewColumns, sparkScale } from '@/components/columns'
import { useScope } from '@/components/layout/insights-controls'
import { PageHeader } from '@/components/page-header'
import { StatCard } from '@/components/stat-card'
import { EmptyState, ErrorState } from '@/components/state'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Card, CardAction, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { Skeleton } from '@/components/ui/skeleton'
import { useApi } from '@/hooks/use-api'
import type { Paged } from '@/lib/api'
import { carry } from '@/lib/scope'
import { SEVERITIES } from '@/lib/severity'
import type { Hotspot, Overview as OverviewData, ReviewRow } from '@/lib/types'

const SEV_COLOR: Record<string, string> = { critical: 'text-sev-critical-solid', error: 'text-sev-error-solid', warning: 'text-sev-warning-solid', info: 'text-sev-info-solid', nit: 'text-sev-nit-solid' }

export default function Overview() {
  const { days, repo } = useScope()
  const { search } = useLocation()
  const scope = carry(search)
  const ov = useApi<OverviewData>('/v1/insights/overview', { days, repo })
  const hot = useApi<Paged<Hotspot>>('/v1/insights/hotspots', { days, repo, page_size: 5 })
  const latest = useApi<Paged<ReviewRow>>('/v1/reviews', { days, repo, page_size: 5 })

  const now = ov.data?.overview
  const before = ov.data?.prior
  const baseline = before !== undefined && !before.empty

  if (ov.error && !ov.data) {
    return (
      <>
        <PageHeader title="Overview" />
        <Card>
          <ErrorState error={ov.error} onRetry={ov.reload} />
        </Card>
      </>
    )
  }

  if (now?.empty) {
    return (
      <>
        <PageHeader title="Overview" description="Findings that reviews reported, and where they concentrate." />
        <Card>
          <EmptyState title="No reviews in this period">
            A review shows up here once nitpick posts its report to this server, with a write token in <code className="text-xs">NITPICK_INSIGHTS_TOKEN</code>.
            <pre className="bg-muted mt-4 overflow-x-auto rounded-lg p-3 text-left text-xs">
              <TerminalIcon className="mr-2 inline size-3.5" aria-hidden />
              nitpick review -format json -insights https://HOST -insights-repo OWNER/NAME
            </pre>
          </EmptyState>
        </Card>
      </>
    )
  }

  const rows = hot.data?.items ?? []
  const stat = (key: 'reviews' | 'findings' | 'errors' | 'repos' | 'files') => ({ value: now?.[key], prior: baseline ? before?.[key] : undefined, days })

  return (
    <>
      <PageHeader title="Overview" description="Findings that reviews reported, and where they concentrate." />

      {now && now.incomplete > 0 && (
        <Alert variant="destructive">
          <AlertTitle>{now.incomplete === 1 ? '1 review did not finish' : `${now.incomplete} reviews did not finish`}</AlertTitle>
          <AlertDescription>
            A review that stopped early reports few or no findings, which is not a clean result.{' '}
            <Link to={{ pathname: '/insights/reviews', search: `${scope}${scope ? '&' : '?'}complete=false` }} className="underline">
              See them
            </Link>
          </AlertDescription>
        </Alert>
      )}

      <section aria-label="Totals" className="grid gap-4 sm:grid-cols-2 xl:grid-cols-5">
        <StatCard label="Reviews" {...stat('reviews')} />
        <StatCard label="Findings" worse {...stat('findings')} />
        <StatCard label="Error or critical" worse {...stat('errors')} />
        <StatCard label="Repositories" hint="Reviewed in the period" {...stat('repos')} />
        <StatCard label="Files with findings" hint="Distinct, per repository" {...stat('files')} />
      </section>
      {before?.empty && <p className="text-muted-foreground -mt-2 text-sm">No reviews came before this period, so there is nothing to compare these totals with.</p>}

      <Card>
        <CardHeader>
          <CardTitle>Findings per day</CardTitle>
          <CardDescription>Error or critical findings at the base of each bar, everything else above.</CardDescription>
        </CardHeader>
        <CardContent>{ov.data ? <DailyChart series={ov.data.series} /> : <Skeleton className="h-64 w-full" />}</CardContent>
      </Card>

      <div className="grid gap-4 lg:grid-cols-2">
        <Card>
          <CardHeader>
            <CardTitle>By severity</CardTitle>
          </CardHeader>
          <CardContent>
            {now ? (
              <Bars rows={[...SEVERITIES].reverse().map((s) => ({ label: s, n: now.by_severity[s] ?? 0, className: SEV_COLOR[s] }))} />
            ) : (
              <Skeleton className="h-32 w-full" />
            )}
          </CardContent>
        </Card>
        <Card>
          <CardHeader>
            <CardTitle>By class</CardTitle>
          </CardHeader>
          <CardContent>{now ? now.by_class.length ? <Bars rows={now.by_class.slice(0, 6).map((c) => ({ label: c.label || 'unclassified', n: c.n }))} /> : <p className="text-muted-foreground text-sm">No findings in this period.</p> : <Skeleton className="h-32 w-full" />}</CardContent>
        </Card>
      </div>

      <Card>
        <CardHeader>
          <CardTitle>Where findings concentrate</CardTitle>
          <CardDescription>The files with the most, and the most recent, severe findings.</CardDescription>
          <CardAction>
            <Button variant="ghost" size="sm" render={<Link to={{ pathname: '/insights/hotspots', search: scope }} />}>
              All hotspots <ArrowRightIcon />
            </Button>
          </CardAction>
        </CardHeader>
        <CardContent className="px-0">
          <DataTable
            bare
            columns={hotspotColumns(sparkScale(rows), (h) => `?path=${encodeURIComponent(h.path)}&repo=${encodeURIComponent(h.repo)}&days=${days}`).filter((c) => c.id !== 'last')}
            rows={rows}
            rowKey={(h) => `${h.repo}\u0000${h.path}`}
            loading={hot.loading}
            error={hot.error}
            onRetry={hot.reload}
            columnsMenu={false}
            placeholderRows={5}
            caption="The five files with the most findings"
            empty={<EmptyState title="No findings in this period" />}
          />
        </CardContent>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle>Latest reviews</CardTitle>
          <CardAction>
            <Button variant="ghost" size="sm" render={<Link to={{ pathname: '/insights/reviews', search: scope }} />}>
              All reviews <ArrowRightIcon />
            </Button>
          </CardAction>
        </CardHeader>
        <CardContent className="px-0">
          <DataTable
            bare
            columns={reviewColumns(scope).filter((c) => ['when', 'repo', 'ref', 'result', 'findings'].includes(c.id))}
            rows={latest.data?.items ?? []}
            rowKey={(r) => r.id}
            loading={latest.loading}
            error={latest.error}
            onRetry={latest.reload}
            columnsMenu={false}
            placeholderRows={5}
            caption="The five latest reviews"
            empty={<EmptyState title="No reviews in this period" />}
          />
        </CardContent>
      </Card>
    </>
  )
}
