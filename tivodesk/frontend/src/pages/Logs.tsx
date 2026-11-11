import {useEffect, useMemo, useRef, useState} from 'react'
import {api, LogEntry, on, Topic} from '../api'

const levels = ['DEBUG', 'INFO', 'WARN', 'ERROR'] as const
const keep = 2000 // matches the engine's ring buffer

export default function Logs() {
    const [entries, setEntries] = useState<LogEntry[]>([])
    const [minLevel, setMinLevel] = useState<typeof levels[number]>('INFO')
    const [follow, setFollow] = useState(true)
    const bottom = useRef<HTMLDivElement>(null)

    useEffect(() => {
        let live = true
        // Subscribe first, then load the backlog, and merge by sequence
        // number so an entry arriving in between is neither lost nor doubled.
        const off = on<LogEntry>(Topic.logEntry, e => {
            setEntries(prev => (prev.length && prev[prev.length - 1].seq >= e.seq ? prev : [...prev, e].slice(-keep)))
        })
        api.logs().then(backlog => {
            if (!live) return
            setEntries(prev => {
                const last = backlog.length ? backlog[backlog.length - 1].seq : 0
                return [...backlog, ...prev.filter(e => e.seq > last)].slice(-keep)
            })
        })
        return () => {
            live = false
            off()
        }
    }, [])

    const shown = useMemo(() => {
        const min = levels.indexOf(minLevel)
        return entries.filter(e => levels.indexOf(e.level) >= min)
    }, [entries, minLevel])

    useEffect(() => {
        if (follow) bottom.current?.scrollIntoView({block: 'end'})
    }, [shown, follow])

    return (
        <div className="logs-page">
            <div className="heading">
                <h1>Logs</h1>
                <div className="row">
                    <label htmlFor="level">Show</label>
                    <select id="level" value={minLevel}
                            onChange={e => setMinLevel(e.target.value as typeof levels[number])}>
                        <option value="DEBUG">Everything</option>
                        <option value="INFO">Info and above</option>
                        <option value="WARN">Warnings and errors</option>
                        <option value="ERROR">Errors only</option>
                    </select>
                    <label className="check">
                        <input type="checkbox" checked={follow} onChange={e => setFollow(e.target.checked)}/>
                        Follow
                    </label>
                </div>
            </div>
            <div className="logs" role="log" aria-live="off">
                {shown.length === 0 && <p className="muted">Nothing to show at this level.</p>}
                {shown.map(e => (
                    <div key={e.seq} className={'log ' + e.level.toLowerCase()}>
                        <time>{new Date(e.time).toLocaleTimeString()}</time>
                        <span className="level">{e.level}</span>
                        <span className="message">{e.message}</span>
                    </div>
                ))}
                <div ref={bottom}/>
            </div>
        </div>
    )
}
