import { SearchIcon, XIcon } from 'lucide-react'
import { useEffect, useState } from 'react'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'

/** A search box that waits for the reader to pause before it asks the server. */
export function SearchInput({ value, onChange, placeholder, label }: { value: string; onChange: (v: string) => void; placeholder: string; label: string }) {
  const [text, setText] = useState(value)
  // The address changed under us (back button, a cleared filter): follow it. This is
  // derived during render, not synced in an effect, so there is no frame of the old text.
  const [seen, setSeen] = useState(value)
  if (value !== seen) {
    setSeen(value)
    setText(value)
  }
  useEffect(() => {
    if (text === value) return
    const t = setTimeout(() => onChange(text.trim()), 300)
    return () => clearTimeout(t)
  }, [text, value, onChange])

  return (
    <div className="relative w-full min-w-48 max-w-72 flex-1">
      <SearchIcon className="text-muted-foreground pointer-events-none absolute top-1/2 left-2.5 size-4 -translate-y-1/2" aria-hidden />
      <Input type="search" value={text} onChange={(e) => setText(e.target.value)} placeholder={placeholder} aria-label={label} className="h-8 pr-8 pl-8 [&::-webkit-search-cancel-button]:hidden" />
      {text && (
        <button type="button" onClick={() => { setText(''); onChange('') }} aria-label="Clear search" className="text-muted-foreground hover:text-foreground absolute top-1/2 right-1.5 grid size-6 -translate-y-1/2 place-items-center rounded-md">
          <XIcon className="size-3.5" />
        </button>
      )}
    </div>
  )
}

export type Option = { value: string; label: string }

/** A filter: one choice from a short list, with "any" as the unfiltered state. */
export function FilterSelect({ label, value, options, onChange, any, className }: { label: string; value: string; options: Option[]; onChange: (v: string) => void; any: string; className?: string }) {
  const items = [{ value: '', label: any }, ...options]
  return (
    <Select items={items} value={value} onValueChange={(v) => onChange(v ?? '')}>
      <SelectTrigger size="sm" className={className ?? 'w-40'} aria-label={label}>
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
  )
}

export function ClearFilters({ show, onClear }: { show: boolean; onClear: () => void }) {
  if (!show) return null
  return (
    <Button variant="ghost" size="sm" onClick={onClear}>
      Reset <XIcon />
    </Button>
  )
}
