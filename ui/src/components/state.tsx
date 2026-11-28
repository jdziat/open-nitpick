import { AlertCircleIcon, InboxIcon, RefreshCwIcon } from 'lucide-react'
import type { ReactNode } from 'react'
import { Button } from '@/components/ui/button'
import { describe } from '@/lib/api'

export function EmptyState({ title, children, action }: { title: string; children?: ReactNode; action?: ReactNode }) {
  return (
    <div className="flex flex-col items-center gap-3 px-6 py-14 text-center">
      <span className="bg-muted text-muted-foreground grid size-10 place-items-center rounded-full">
        <InboxIcon className="size-5" aria-hidden />
      </span>
      <div className="space-y-1">
        <p className="font-medium">{title}</p>
        {children && <div className="text-muted-foreground mx-auto max-w-md text-sm">{children}</div>}
      </div>
      {action}
    </div>
  )
}

export function ErrorState({ error, onRetry }: { error: unknown; onRetry?: () => void }) {
  return (
    <div role="alert" className="flex flex-col items-center gap-3 px-6 py-14 text-center">
      <span className="bg-sev-critical-bg text-sev-critical grid size-10 place-items-center rounded-full">
        <AlertCircleIcon className="size-5" aria-hidden />
      </span>
      <div className="space-y-1">
        <p className="font-medium">This could not be loaded</p>
        <p className="text-muted-foreground mx-auto max-w-md text-sm">{describe(error)}</p>
      </div>
      {onRetry && (
        <Button variant="outline" size="sm" onClick={onRetry}>
          <RefreshCwIcon /> Try again
        </Button>
      )}
    </div>
  )
}
