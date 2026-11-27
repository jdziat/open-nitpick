// The shapes the insights and notary routes answer with. They mirror the Go
// structs' JSON tags; the server is the source of truth.
export type Finding = {
  path: string
  line: number
  end_line?: number
  severity: string
  class: string
  category?: string
  title: string
  rationale?: string
}

export type FindingRow = Finding & { repo: string; review_id: string; commit?: string; at: string }

export type ReviewRow = {
  id: string
  repo: string
  ref?: string
  commit?: string
  result: string
  at: string
  complete: boolean
  findings: number
  errors: number
  worst?: string
  withheld?: number
}

export type Review = {
  id: string
  repo: string
  ref?: string
  commit?: string
  at: string
  result: string
  complete: boolean
  findings: Finding[]
  truncated?: number
  withheld?: number
  source?: string
}

export type RepoRow = { repo: string; reviews: number; findings: number; errors: number; last: string }

export type Hotspot = {
  repo: string
  path: string
  score: number
  findings: number
  recent: number
  prior: number
  worst: string
  trend: 'up' | 'down' | 'flat'
  baseline: boolean
  delta: number
  last: string
  spark: number[]
}

export type OverviewTotals = {
  reviews: number
  findings: number
  errors: number
  repos: number
  files: number
  incomplete: number
  empty: boolean
  by_severity: Record<string, number>
  by_class: { label: string; n: number }[]
}

export type Overview = {
  overview: OverviewTotals
  prior: OverviewTotals
  series: { day: string; reviews: number; findings: number; errors: number }[]
}

export type Facets = { repos: string[]; classes: string[]; severities: string[] }

export type RecordSummary = {
  hash: string
  created: string
  tool: string
  provider: string
  model: string
  source: string
  effort?: string
  complete: boolean
  batches: number
  answers: number
  clears: number
}

export type RecordView = {
  hash: string
  tool: string
  provider: string
  model: string
  source: string
  effort: string
  complete: boolean
  created: string
  clears: number
  cleared_by: string[]
  answers: { request: string; slot: string; text: string; unusable: boolean }[]
}
