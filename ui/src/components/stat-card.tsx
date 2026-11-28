import { ArrowDownRightIcon, ArrowUpRightIcon } from 'lucide-react'
import { Card, CardContent } from '@/components/ui/card'
import { Skeleton } from '@/components/ui/skeleton'
import { cn } from '@/lib/utils'
import { formatNumber } from '@/lib/format'

type Props = {
  label: string
  value?: number
  prior?: number
  /** More of this is bad news: a rise is red and a fall is green. Reviews are activity, not a problem. */
  worse?: boolean
  hint?: string
  days: string
}

export function StatCard({ label, value, prior, worse, hint, days }: Props) {
  const delta = value !== undefined && prior !== undefined ? value - prior : undefined
  const Icon = delta !== undefined && delta < 0 ? ArrowDownRightIcon : ArrowUpRightIcon
  return (
    <Card size="sm">
      <CardContent className="space-y-1">
        <p className="text-muted-foreground text-sm font-medium">{label}</p>
        {value === undefined ? <Skeleton className="h-8 w-20" /> : <p className="text-3xl font-semibold tracking-tight tabular-nums">{formatNumber(value)}</p>}
        <p className="text-muted-foreground min-h-5 text-xs">
          {delta !== undefined && delta !== 0 ? (
            <span
              className={cn(
                'inline-flex items-center gap-0.5 font-medium',
                worse && delta > 0 && 'text-sev-critical',
                worse && delta < 0 && 'text-success',
              )}
            >
              <Icon className="size-3" aria-hidden />
              {delta > 0 ? '+' : '−'}
              {formatNumber(Math.abs(delta))}
              <span className="text-muted-foreground font-normal"> vs the {days} days before</span>
            </span>
          ) : delta === 0 ? (
            `Same as the ${days} days before`
          ) : (
            hint
          )}
        </p>
      </CardContent>
    </Card>
  )
}
