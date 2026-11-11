// Small formatting helpers shared by the pages.

export function size(bytes: number): string {
    if (bytes >= 1e9) return (bytes / 1e9).toFixed(bytes >= 1e10 ? 0 : 1) + ' GB'
    if (bytes >= 1e6) return Math.round(bytes / 1e6) + ' MB'
    return Math.max(1, Math.round(bytes / 1e3)) + ' KB'
}

export function duration(ms: number): string {
    const minutes = Math.round(ms / 60000)
    if (minutes < 60) return `${minutes} min`
    const h = Math.floor(minutes / 60), m = minutes % 60
    return m ? `${h} h ${m} min` : `${h} h`
}

const dateOnly = new Intl.DateTimeFormat(undefined, {year: 'numeric', month: 'short', day: 'numeric'})
const dateTime = new Intl.DateTimeFormat(undefined, {
    weekday: 'short', year: 'numeric', month: 'short', day: 'numeric', hour: 'numeric', minute: '2-digit',
})

// Go sends a zero time as year 1; treat anything that old as "no date".
function valid(iso: string): Date | null {
    const d = new Date(iso)
    return isNaN(d.getTime()) || d.getFullYear() < 1990 ? null : d
}

export function shortDate(iso: string): string {
    const d = valid(iso)
    return d ? dateOnly.format(d) : ''
}

export function longDate(iso: string): string {
    const d = valid(iso)
    return d ? dateTime.format(d) : ''
}

export function ago(iso: string): string {
    const d = valid(iso)
    if (!d) return 'never'
    const s = Math.max(0, Math.round((Date.now() - d.getTime()) / 1000))
    if (s < 10) return 'just now'
    if (s < 60) return `${s} seconds ago`
    if (s < 3600) return `${Math.round(s / 60)} min ago`
    return longDate(iso)
}

export function plural(n: number, one: string, many: string): string {
    return `${n.toLocaleString()} ${n === 1 ? one : many}`
}
