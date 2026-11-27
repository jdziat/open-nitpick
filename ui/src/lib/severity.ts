export const SEVERITIES = ['nit', 'info', 'warning', 'error', 'critical'] as const
export type Severity = (typeof SEVERITIES)[number]

export const severityRank = (s: string) => SEVERITIES.indexOf(s as Severity)
