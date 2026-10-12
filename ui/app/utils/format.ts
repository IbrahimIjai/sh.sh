const numberFormat = new Intl.NumberFormat('en')
const compactFormat = new Intl.NumberFormat('en', { notation: 'compact', maximumFractionDigits: 1 })
const dateFormat = new Intl.DateTimeFormat('en', { day: 'numeric', month: 'short', year: 'numeric' })
const dayFormat = new Intl.DateTimeFormat('en', { day: 'numeric', month: 'short', timeZone: 'UTC' })

export function formatNumber(value: number) {
  return value >= 10_000 ? compactFormat.format(value) : numberFormat.format(value)
}

export function formatDate(value: string) {
  return dateFormat.format(new Date(value))
}

export function formatDay(value: string) {
  return dayFormat.format(new Date(`${value}T00:00:00Z`))
}

export function displayUrl(url: string) {
  return url.replace(/^https?:\/\//, '').replace(/\/$/, '')
}
