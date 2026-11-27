import { ArrowLeftIcon } from 'lucide-react'
import { Link, useLocation, useParams } from 'react-router'
import { RepoName, ResultBadge } from '@/components/columns'
import { usePageTitle } from '@/components/layout/page-title'
import { PageHeader } from '@/components/page-header'
import { SeverityBadge } from '@/components/severity-badge'
import { EmptyState, ErrorState } from '@/components/state'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent } from '@/components/ui/card'
import { Skeleton } from '@/components/ui/skeleton'
import { useApi } from '@/hooks/use-api'
import { ApiError } from '@/lib/api'
import { formatDateTime } from '@/lib/format'
import { carry } from '@/lib/scope'
import { severityRank } from '@/lib/severity'
import type { Review } from '@/lib/types'

export function reviewTitle(r: Review) {
  return `${r.repo}${r.ref ? ` · ${r.ref}` : r.commit ? ` · ${r.commit.slice(0, 8)}` : ''}`
}

export default function ReviewDetail() {
  const { id = '' } = useParams()
  const { search } = useLocation()
  const { data, error, loading, reload } = useApi<Review>(`/v1/reviews/${encodeURIComponent(id)}`)
  usePageTitle(data ? reviewTitle(data) : undefined)
  const back = (
    <Button variant="ghost" size="sm" render={<Link to={{ pathname: '/insights/reviews', search: carry(search) }} />}>
      <ArrowLeftIcon /> All reviews
    </Button>
  )

  if (error instanceof ApiError && error.status === 404) {
    return (
      <>
        {back}
        <Card>
          <EmptyState title="There is no such review">It may have been sent to a different server, or the link is wrong.</EmptyState>
        </Card>
      </>
    )
  }
  if (error && !data) {
    return (
      <>
        {back}
        <Card>
          <ErrorState error={error} onRetry={reload} />
        </Card>
      </>
    )
  }
  if (!data || loading) {
    return (
      <>
        {back}
        <Skeleton className="h-8 w-72" />
        <Skeleton className="h-40 w-full" />
      </>
    )
  }

  const findings = [...data.findings].sort((a, b) => severityRank(b.severity) - severityRank(a.severity))
  return (
    <>
      {back}
      <PageHeader title={reviewTitle(data)} description={`Reported ${formatDateTime(data.at)}`} actions={<ResultBadge complete={data.complete} result={data.result} />} />
      {!data.complete && (
        <Alert variant="destructive">
          <AlertTitle>This review stopped early</AlertTitle>
          <AlertDescription>Its findings may be incomplete. {data.findings.length === 0 && 'No findings were reported before it stopped, which is not a clean result.'}</AlertDescription>
        </Alert>
      )}
      <Card size="sm">
        <CardContent>
          <dl className="grid gap-x-8 gap-y-3 text-sm sm:grid-cols-2 lg:grid-cols-4">
            <Fact label="Repository">
              <RepoName repo={data.repo} />
            </Fact>
            {data.ref && <Fact label="Ref">{data.ref}</Fact>}
            {data.commit && (
              <Fact label="Commit">
                <code className="text-xs break-all">{data.commit}</code>
              </Fact>
            )}
            <Fact label="Findings">
              {data.findings.length}
              {data.truncated ? <span className="text-muted-foreground"> (and {data.truncated} more that were not kept)</span> : null}
            </Fact>
            {data.withheld ? <Fact label="Withheld">{data.withheld} drafted and held back by a later stage</Fact> : null}
            {data.source && <Fact label="Posted by">{data.source}</Fact>}
          </dl>
        </CardContent>
      </Card>

      <h2 className="text-lg font-semibold tracking-tight">Findings</h2>
      {findings.length === 0 ? (
        <Card>
          <EmptyState title={data.complete ? 'The review finished and reported no findings' : 'No findings were reported before the review stopped'} />
        </Card>
      ) : (
        <ul className="space-y-3">
          {findings.map((f, i) => (
            <li key={`${f.path}:${f.line}:${i}`}>
              <Card size="sm">
                <CardContent className="space-y-2">
                  <div className="flex flex-wrap items-center gap-2">
                    <SeverityBadge severity={f.severity} />
                    {f.class && <Badge variant="outline" className="font-normal">{f.class}</Badge>}
                    <code className="text-muted-foreground text-xs break-all">
                      {f.path}
                      {f.line ? `:${f.line}` : ''}
                    </code>
                  </div>
                  <p className="font-medium">{f.title}</p>
                  {f.rationale && <p className="text-muted-foreground max-w-3xl text-sm leading-relaxed whitespace-pre-wrap">{f.rationale}</p>}
                </CardContent>
              </Card>
            </li>
          ))}
        </ul>
      )}
    </>
  )
}

function Fact({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div className="space-y-0.5">
      <dt className="text-muted-foreground text-xs">{label}</dt>
      <dd>{children}</dd>
    </div>
  )
}
