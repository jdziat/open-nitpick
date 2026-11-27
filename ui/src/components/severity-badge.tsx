import { cn } from '@/lib/utils'

const styles: Record<string, string> = {
  critical: 'bg-sev-critical-bg text-sev-critical',
  error: 'bg-sev-error-bg text-sev-error',
  warning: 'bg-sev-warning-bg text-sev-warning',
  info: 'bg-sev-info-bg text-sev-info',
  nit: 'bg-sev-nit-bg text-sev-nit',
}

const dots: Record<string, string> = {
  critical: 'bg-sev-critical-solid',
  error: 'bg-sev-error-solid',
  warning: 'bg-sev-warning-solid',
  info: 'bg-sev-info-solid',
  nit: 'bg-sev-nit-solid',
}

// Severity is its word first and its color second: the badge reads the same in
// greyscale, and the dot only repeats what the text says.
export function SeverityBadge({ severity, className }: { severity: string; className?: string }) {
  return (
    <span
      className={cn(
        'inline-flex h-5 items-center gap-1.5 rounded-md px-2 text-xs font-medium whitespace-nowrap capitalize',
        styles[severity] ?? styles.nit,
        className,
      )}
    >
      <span aria-hidden className={cn('size-1.5 rounded-full', dots[severity] ?? dots.nit)} />
      {severity}
    </span>
  )
}
