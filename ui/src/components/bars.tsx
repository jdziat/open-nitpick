// A horizontal bar per label, drawn as SVG so its widths are attributes and not
// styles. The number is printed beside it: the bar is the quick read, not the value.
export function Bars({ rows, colorClass }: { rows: { label: string; n: number; className?: string }[]; colorClass?: string }) {
  const most = Math.max(...rows.map((r) => r.n), 1)
  return (
    <ul className="space-y-2.5">
      {rows.map((r) => (
        <li key={r.label} className="grid grid-cols-[6.5rem_1fr_3rem] items-center gap-3 text-sm">
          <span className="truncate capitalize">{r.label}</span>
          <svg viewBox="0 0 100 8" preserveAspectRatio="none" className={`h-2 w-full ${r.className ?? colorClass ?? 'text-brand'}`} aria-hidden>
            <rect width="100" height="8" rx="2" className="fill-muted" />
            {r.n > 0 && <rect width={Math.max((r.n / most) * 100, 2)} height="8" rx="2" className="fill-current" />}
          </svg>
          <span className="text-right tabular-nums">{r.n}</span>
        </li>
      ))}
    </ul>
  )
}
