import { useApi } from '@/hooks/use-api'
import { useSearchState } from '@/hooks/use-search-state'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'
import { ToggleGroup, ToggleGroupItem } from '@/components/ui/toggle-group'

export const PERIODS = ['7', '30', '90'] as const
export const DEFAULT_DAYS = '30'

type Facets = { repos: string[]; classes: string[]; severities: string[] }

/** The period and repository every insights page is read through. They live in the address. */
export function useScope() {
  const { get } = useSearchState({ days: DEFAULT_DAYS })
  return { days: get('days'), repo: get('repo') }
}

export function InsightsControls() {
  const { get, set } = useSearchState({ days: DEFAULT_DAYS })
  const days = get('days')
  const repo = get('repo')
  const { data } = useApi<Facets>('/v1/insights/facets', { days })
  const repos = data?.repos ?? []
  const items = [{ value: '', label: 'All repositories' }, ...repos.map((r) => ({ value: r, label: r }))]

  return (
    <div className="flex flex-wrap items-center gap-2">
      <ToggleGroup
        value={[days]}
        onValueChange={(v) => v[0] && set({ days: v[0] }, { keepPage: false })}
        variant="outline"
        size="sm"
        spacing={0}
        aria-label="Period"
      >
        {PERIODS.map((p) => (
          <ToggleGroupItem key={p} value={p} aria-label={`${p} days`} className="px-3">
            {p}d
          </ToggleGroupItem>
        ))}
      </ToggleGroup>
      <Select items={items} value={repo} onValueChange={(v) => set({ repo: v ?? '' })}>
        <SelectTrigger size="sm" className="w-44 max-w-full" aria-label="Repository">
          <SelectValue />
        </SelectTrigger>
        <SelectContent>
          {items.map((i) => (
            <SelectItem key={i.value} value={i.value}>
              {i.label}
            </SelectItem>
          ))}
        </SelectContent>
      </Select>
    </div>
  )
}
