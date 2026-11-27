import { ArrowDownRightIcon, ArrowUpRightIcon, MinusIcon } from 'lucide-react'
import { cn } from '@/lib/utils'

type Props = { trend: 'up' | 'down' | 'flat'; baseline: boolean; delta: number; prior: number }

/**
 * A file's direction against the period before. More findings is bad news, so up is
 * red and down is green; a change inside the steady band, or a period with nothing
 * before it, is neither. The arrow and the words say the same thing as the color.
 */
export function Trend({ trend, baseline, delta, prior }: Props) {
  if (!baseline) return <span className="text-muted-foreground text-sm">No earlier data</span>
  const Icon = trend === 'up' ? ArrowUpRightIcon : trend === 'down' ? ArrowDownRightIcon : MinusIcon
  return (
    <span
      className={cn(
        'inline-flex items-center gap-1 text-sm whitespace-nowrap',
        trend === 'up' && 'text-sev-critical font-medium',
        trend === 'down' && 'text-success font-medium',
        trend === 'flat' && 'text-muted-foreground',
      )}
    >
      <Icon className="size-3.5" aria-hidden />
      {trend === 'flat' ? 'Steady' : `${delta > 0 ? '+' : '−'}${Math.abs(delta)}`}
      <span className="text-muted-foreground font-normal">(was {prior})</span>
    </span>
  )
}
