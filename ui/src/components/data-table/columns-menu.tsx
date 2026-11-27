import { Columns3Icon } from 'lucide-react'
import { Button } from '@/components/ui/button'
import { DropdownMenu, DropdownMenuCheckboxItem, DropdownMenuContent, DropdownMenuGroup, DropdownMenuLabel, DropdownMenuTrigger } from '@/components/ui/dropdown-menu'

export type ColumnChoice = { id: string; header: string; visible: boolean }

export function ColumnsMenu({ columns, onToggle }: { columns: ColumnChoice[]; onToggle: (id: string, visible: boolean) => void }) {
  if (columns.length === 0) return null
  return (
    <DropdownMenu>
      <DropdownMenuTrigger render={<Button variant="outline" size="sm" />}>
        <Columns3Icon /> Columns
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end" className="min-w-44">
        <DropdownMenuGroup>
          <DropdownMenuLabel>Show columns</DropdownMenuLabel>
          {columns.map((c) => (
            <DropdownMenuCheckboxItem key={c.id} checked={c.visible} onCheckedChange={(v) => onToggle(c.id, !!v)}>
              {c.header}
            </DropdownMenuCheckboxItem>
          ))}
        </DropdownMenuGroup>
      </DropdownMenuContent>
    </DropdownMenu>
  )
}
