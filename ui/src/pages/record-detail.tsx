import { ArrowLeftIcon } from 'lucide-react'
import { Link, useParams } from 'react-router'
import { When } from '@/components/columns'
import { PageHeader } from '@/components/page-header'
import { EmptyState, ErrorState } from '@/components/state'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent } from '@/components/ui/card'
import { Skeleton } from '@/components/ui/skeleton'
import { useApi } from '@/hooks/use-api'
import { ApiError } from '@/lib/api'
import type { RecordView } from '@/lib/types'

export default function RecordDetail() {
  const { hash = '' } = useParams()
  const { data, error, reload } = useApi<RecordView>(`/v1/records/view/${encodeURIComponent(hash)}`)
  const back = (
    <Button variant="ghost" size="sm" className="-ml-2 w-fit" render={<Link to="/notary" />}>
      <ArrowLeftIcon /> All records
    </Button>
  )
  if (error instanceof ApiError && error.status === 404) {
    return (
      <>
        {back}
        <Card>
          <EmptyState title="There is no such record" />
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
  if (!data) {
    return (
      <>
        {back}
        <Skeleton className="h-8 w-72" />
        <Skeleton className="h-48 w-full" />
      </>
    )
  }
  return (
    <>
      {back}
      <PageHeader
        title={data.model}
        description={<code className="text-xs break-all">{data.hash}</code>}
        actions={data.complete ? <Badge variant="secondary">Replays</Badge> : <Badge variant="outline">Resumes</Badge>}
      />
      <Card size="sm">
        <CardContent>
          <dl className="grid gap-x-8 gap-y-3 text-sm sm:grid-cols-2 lg:grid-cols-4">
            <Fact label="Provider">{data.provider}</Fact>
            <Fact label="Tool version">{data.tool}</Fact>
            <Fact label="Reasoning level">{data.effort || <span className="text-muted-foreground">Not recorded</span>}</Fact>
            <Fact label="Written by">{data.source}</Fact>
            <Fact label="Created">
              <When at={data.created} />
            </Fact>
            <Fact label="Cleared">
              {data.clears} time{data.clears === 1 ? '' : 's'}
              {data.cleared_by.length > 0 && <span className="text-muted-foreground">, by {data.cleared_by.join(', ')}</span>}
            </Fact>
          </dl>
        </CardContent>
      </Card>
      <p className="text-muted-foreground max-w-3xl text-sm">
        {data.complete ? 'A review with these inputs is replayed from this record.' : 'A review with these inputs resumes from this record, asking only for the answers it lacks.'} A record holds the answers, keyed by a hash of the request they answer and a slot. It does not hold the prompts or which stage asked. An answer marked unusable was a reply the review rejected or retried.
      </p>
      <h2 className="text-lg font-semibold tracking-tight">Answers ({data.answers.length})</h2>
      {data.answers.length === 0 ? (
        <Card>
          <EmptyState title="The record holds no answers" />
        </Card>
      ) : (
        <ul className="space-y-3">
          {data.answers.map((a) => (
            <li key={`${a.request}#${a.slot}`}>
              <Card size="sm">
                <CardContent className="space-y-2">
                  <div className="flex flex-wrap items-center gap-2 text-sm">
                    <span className="text-muted-foreground">Request</span>
                    <code className="text-xs break-all">{a.request}</code>
                    {a.slot && <Badge variant="outline" className="font-normal">answer {a.slot}</Badge>}
                    {a.unusable && <Badge variant="destructive">Unusable</Badge>}
                  </div>
                  <pre className="bg-muted max-h-96 overflow-auto rounded-lg p-3 text-xs whitespace-pre-wrap break-words">{a.text}</pre>
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
