import { ChevronDownIcon } from 'lucide-react'
import { useState } from 'react'
import { Bar, BarChart, CartesianGrid, XAxis, YAxis } from 'recharts'
import { Button } from '@/components/ui/button'
import { ChartContainer, ChartLegend, ChartLegendContent, ChartTooltip, ChartTooltipContent, type ChartConfig } from '@/components/ui/chart'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import { formatDay } from '@/lib/format'

type Day = { day: string; reviews: number; findings: number; errors: number }

// Two series, told apart by color and by the legend and tooltip that name them:
// error or critical findings at the base, everything else above. The pair passes
// the colour-vision and contrast checks in both themes.
const config = {
  errors: { label: 'Error or critical', color: 'var(--chart-2)' },
  other: { label: 'Other findings', color: 'var(--chart-1)' },
} satisfies ChartConfig

export function DailyChart({ series }: { series: Day[] }) {
  const [table, setTable] = useState(false)
  const data = series.map((d) => ({ day: d.day, errors: d.errors, other: d.findings - d.errors, reviews: d.reviews }))
  const most = Math.max(...series.map((d) => d.findings), 0)
  return (
    <div className="space-y-3">
      <ChartContainer config={config} className="chart-findings aspect-auto h-64 w-full" role="img" aria-label={`Findings per day, ${series.length} days, at most ${most} a day`}>
        <BarChart data={data} margin={{ left: 0, right: 8, top: 8 }} barCategoryGap="18%">
          <CartesianGrid vertical={false} />
          <XAxis dataKey="day" tickLine={false} axisLine={false} tickMargin={8} minTickGap={24} tickFormatter={formatDay} />
          <YAxis tickLine={false} axisLine={false} width={32} allowDecimals={false} />
          <ChartTooltip cursor={{ fillOpacity: 0.08 }} content={<ChartTooltipContent labelFormatter={(_, p) => (p[0] ? formatDay(String(p[0].payload.day)) : '')} />} />
          <ChartLegend content={<ChartLegendContent />} />
          <Bar dataKey="errors" stackId="a" fill="var(--color-errors)" radius={[0, 0, 2, 2]} />
          <Bar dataKey="other" stackId="a" fill="var(--color-other)" radius={[2, 2, 0, 0]} />
        </BarChart>
      </ChartContainer>
      <Button variant="ghost" size="sm" onClick={() => setTable((t) => !t)} aria-expanded={table}>
        <ChevronDownIcon className={table ? 'rotate-180' : ''} />
        {table ? 'Hide the table' : 'Show as a table'}
      </Button>
      {table && (
        <div className="max-h-72 overflow-auto rounded-lg border">
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Day</TableHead>
                <TableHead className="text-right">Reviews</TableHead>
                <TableHead className="text-right">Findings</TableHead>
                <TableHead className="text-right">Error or critical</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {series.map((d) => (
                <TableRow key={d.day}>
                  <TableCell>{formatDay(d.day)}</TableCell>
                  <TableCell className="text-right tabular-nums">{d.reviews}</TableCell>
                  <TableCell className="text-right tabular-nums">{d.findings}</TableCell>
                  <TableCell className="text-right tabular-nums">{d.errors}</TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </div>
      )}
    </div>
  )
}
