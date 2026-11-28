import { Link } from 'react-router'
import { EmptyState } from '@/components/state'
import { Button } from '@/components/ui/button'
import { Card } from '@/components/ui/card'
import { hasComponent, hasRole } from '@/lib/session'

export function home(): string | null {
  if (hasComponent('insights') && hasRole('insights')) return '/insights'
  if (hasComponent('notary') && hasRole('authoritative')) return '/notary'
  return null
}

export default function NotFound() {
  const to = home()
  return (
    <Card>
      <EmptyState title="There is nothing at this address" action={to ? <Button variant="outline" size="sm" render={<Link to={to} />}>Go to the start page</Button> : undefined}>
        The link may be old, or the page may need a role this account does not hold.
      </EmptyState>
    </Card>
  )
}

export function NoAccess() {
  return (
    <Card>
      <EmptyState title="This account holds no role that opens a page">Ask whoever runs this server to grant the insights or the authoritative role. A sign-in alone grants nothing.</EmptyState>
    </Card>
  )
}
