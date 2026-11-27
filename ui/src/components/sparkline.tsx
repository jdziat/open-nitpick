// A small line of findings over the period. Rows of one table share a scale, so a
// file with 3 findings does not look as busy as one with 41. It is drawn from the
// numbers, with the numbers as its text, because it is a picture of a list.
export function Sparkline({ values, scale }: { values: number[]; scale: number }) {
  const w = 96
  const h = 24
  const most = Math.max(scale, ...values, 1)
  const label = `Findings per part of the period, oldest first: ${values.join(', ')}`
  if (values.every((v) => v === 0)) return <span className="text-muted-foreground text-sm">None</span>
  const pts = values.map((v, i) => [2 + (i / Math.max(values.length - 1, 1)) * (w - 4), h - 3 - (v / most) * (h - 6)] as const)
  const last = pts[pts.length - 1]
  return (
    <svg viewBox={`0 0 ${w} ${h}`} width={w} height={h} role="img" aria-label={label} className="text-brand">
      <title>{label}</title>
      <polyline points={pts.map((p) => p.map((n) => n.toFixed(1)).join(',')).join(' ')} fill="none" stroke="currentColor" strokeWidth="1.75" strokeLinejoin="round" strokeLinecap="round" />
      <circle cx={last[0]} cy={last[1]} r="2.5" fill="currentColor" />
    </svg>
  )
}
