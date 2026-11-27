import { Link } from 'react-router'
import type { Column } from '@/components/data-table/data-table'
import { SeverityBadge } from '@/components/severity-badge'
import { Sparkline } from '@/components/sparkline'
import { Trend } from '@/components/trend'
import { Badge } from '@/components/ui/badge'
import { formatDateTime, formatNumber, formatRelative, shortCommit } from '@/lib/format'
import type { Hotspot, ReviewRow } from '@/lib/types'

/** A time shown as "2 hours ago", with the exact time on hover and for a screen reader. */
export function When({ at }: { at: string }) {
  return (
    <time dateTime={at} title={formatDateTime(at)} className="text-muted-foreground whitespace-nowrap">
      {formatRelative(at)}
    </time>
  )
}

export function RepoName({ repo }: { repo: string }) {
  return <span className="font-mono text-[0.8rem]">{repo}</span>
}

export function ResultBadge({ complete, result }: { complete: boolean; result: string }) {
  return complete ? (
    <Badge variant="secondary" className="font-normal capitalize">
      {result === 'findings' ? 'Finished' : result}
    </Badge>
  ) : (
    <Badge variant="destructive" title="The review stopped early, so its findings may be incomplete.">
      Did not finish
    </Badge>
  )
}

export function reviewColumns(search: string): Column<ReviewRow>[] {
  return [
    {
      id: 'when',
      header: 'When',
      sort: 'at',
      required: true,
      cell: (r) => (
        <Link to={{ pathname: `/insights/reviews/${r.id}`, search }} className="hover:underline">
          <When at={r.at} />
        </Link>
      ),
    },
    { id: 'repo', header: 'Repository', sort: 'repo', cell: (r) => <RepoName repo={r.repo} /> },
    { id: 'ref', header: 'Ref', className: 'hidden md:table-cell', cell: (r) => r.ref ?? <span className="text-muted-foreground">–</span> },
    { id: 'commit', header: 'Commit', className: 'hidden lg:table-cell', cell: (r) => <code className="text-xs">{shortCommit(r.commit)}</code> },
    { id: 'result', header: 'Result', sort: 'result', cell: (r) => <ResultBadge complete={r.complete} result={r.result} /> },
    { id: 'findings', header: 'Findings', sort: 'findings', align: 'right', cell: (r) => formatNumber(r.findings) },
    { id: 'errors', header: 'Error or critical', sort: 'errors', align: 'right', className: 'hidden sm:table-cell', cell: (r) => formatNumber(r.errors) },
    { id: 'worst', header: 'Worst', className: 'hidden lg:table-cell', cell: (r) => (r.worst ? <SeverityBadge severity={r.worst} /> : <span className="text-muted-foreground">None</span>) },
  ]
}

/** The scale every sparkline in one table is drawn to: the busiest bucket of any row. */
export const sparkScale = (rows: Hotspot[]) => Math.max(1, ...rows.flatMap((h) => h.spark))

export function hotspotColumns(scale: number, findingsSearch: (h: Hotspot) => string): Column<Hotspot>[] {
  return [
    {
      id: 'file',
      header: 'File',
      sort: 'path',
      required: true,
      cell: (h) => (
        <div className="min-w-0">
          <Link to={{ pathname: '/insights/findings', search: findingsSearch(h) }} className="font-mono text-[0.8rem] break-all hover:underline">
            {h.path}
          </Link>
          <div className="text-muted-foreground text-xs">{h.repo}</div>
        </div>
      ),
    },
    { id: 'score', header: 'Score', sort: 'score', align: 'right', cell: (h) => h.score.toFixed(1) },
    { id: 'findings', header: 'Findings', sort: 'findings', align: 'right', cell: (h) => formatNumber(h.findings) },
    { id: 'worst', header: 'Worst', sort: 'worst', cell: (h) => <SeverityBadge severity={h.worst} /> },
    { id: 'trend', header: 'Trend', sort: 'delta', cell: (h) => <Trend trend={h.trend} baseline={h.baseline} delta={h.delta} prior={h.prior} /> },
    { id: 'spark', header: 'Over the period', className: 'hidden md:table-cell', cell: (h) => <Sparkline values={h.spark} scale={scale} /> },
    { id: 'last', header: 'Last seen', sort: 'last', className: 'hidden lg:table-cell', cell: (h) => <When at={h.last} /> },
  ]
}
