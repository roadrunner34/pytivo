import {useCallback, useEffect, useMemo, useRef, useState} from 'react'
import {useVirtualizer} from '@tanstack/react-virtual'
import {AppState} from '../App'
import {api, errorText, NowPlaying, NowPlayingStatus, on, Recording, Topic, Transfer} from '../api'
import {ago, duration, longDate, plural, shortDate, size} from '../format'
import {percent} from './Transfers'

type Sort = 'recent' | 'title'

// One line of the list: a title that groups several recordings, or a
// single recording.
type Row =
    | { kind: 'group'; key: string; title: string; items: Recording[]; open: boolean; bytes: number; newest: string }
    | { kind: 'recording'; key: string; rec: Recording; nested: boolean }

const statusLabel: Record<Recording['status'], string> = {
    normal: '',
    protected: 'Copy protected',
    expiring: 'Expires soon',
    expired: 'Expired',
    kuid: 'Keep until I delete',
    suggestion: 'Suggestion',
    inprogress: 'Recording now',
}

const empty: NowPlaying = {
    tsn: '', loading: false, loaded: 0, total: 0, error: '', fetched: '', unchanged: false, recordings: [],
}

// A list older than this is checked with the TiVo when the page opens.
// The check is one small request if nothing has changed.
const staleAfterMs = 30_000

function stale(fetched: string): boolean {
    const t = new Date(fetched).getTime()
    return isNaN(t) || new Date(fetched).getFullYear() < 1990 || Date.now() - t > staleAfterMs
}

// Why a recording cannot be downloaded, or an empty string if it can.
function blocked(r: Recording): string {
    if (r.copyProtected) return 'This recording is copy protected, so the TiVo will not let it be downloaded.'
    if (r.inProgress) return 'This is still being recorded. It can be downloaded when it finishes.'
    return ''
}

const transferMark: Record<Transfer['state'], string> = {
    queued: '↓', running: '↓', done: '✓', failed: '!', cancelled: '',
}

export default function NowPlayingPage({tivos, tsn, setTSN, hasMAK, go, transfers}: AppState) {
    const [data, setData] = useState<NowPlaying>(empty)
    const [error, setError] = useState('')
    const [notice, setNotice] = useState('')
    const [query, setQuery] = useState('')
    const [sort, setSort] = useState<Sort>('recent')
    const [open, setOpen] = useState<Set<string>>(new Set())
    const [selected, setSelected] = useState<Recording | null>(null)
    const scroller = useRef<HTMLDivElement>(null)

    const refresh = useCallback(async (target: string) => {
        try {
            setError('')
            await api.refreshNowPlaying(target)
        } catch (e) {
            setError(errorText(e))
        }
    }, [])

    // Show what the engine already holds for this TiVo, and fetch if it
    // holds nothing yet. Progress events keep the status current; the full
    // list is re-read when a load finishes.
    useEffect(() => {
        if (!tsn) return
        let live = true
        setSelected(null)
        setOpen(new Set())
        api.nowPlaying(tsn).then(np => {
            if (!live) return
            setData(np)
            if (!np.loading && hasMAK && stale(np.fetched)) refresh(tsn)
        }).catch(e => live && setError(errorText(e)))

        const off = on<NowPlayingStatus>(Topic.nowPlayingChanged, status => {
            if (!live || status.tsn !== tsn) return
            if (status.loading) {
                setData(prev => ({...prev, ...status}))
            } else {
                api.nowPlaying(tsn).then(np => live && setData(np))
            }
        })
        return () => {
            live = false
            off()
        }
    }, [tsn, hasMAK, refresh])

    const rows = useMemo<Row[]>(() => {
        const needle = query.trim().toLowerCase()
        const groups = new Map<string, Recording[]>()
        for (const r of data.recordings) {
            if (needle && !(r.title + ' ' + r.episodeTitle + ' ' + r.description).toLowerCase().includes(needle)) continue
            const list = groups.get(r.title)
            if (list) list.push(r)
            else groups.set(r.title, [r])
        }
        const sorted = [...groups.entries()].map(([title, items]) => {
            items.sort((a, b) => b.captured.localeCompare(a.captured))
            return {title, items, newest: items[0].captured, bytes: items.reduce((n, r) => n + r.size, 0)}
        })
        sorted.sort(sort === 'title'
            ? (a, b) => a.title.localeCompare(b.title, undefined, {sensitivity: 'base'})
            : (a, b) => b.newest.localeCompare(a.newest))

        const out: Row[] = []
        for (const g of sorted) {
            if (g.items.length === 1) {
                out.push({kind: 'recording', key: g.items[0].id, rec: g.items[0], nested: false})
                continue
            }
            // While searching, show every match without making the user expand.
            const isOpen = open.has(g.title) || needle !== ''
            out.push({kind: 'group', key: 'g:' + g.title, title: g.title, items: g.items, open: isOpen, bytes: g.bytes, newest: g.newest})
            if (isOpen) for (const r of g.items) out.push({kind: 'recording', key: r.id, rec: r, nested: true})
        }
        return out
    }, [data.recordings, query, sort, open])

    const virtual = useVirtualizer({
        count: rows.length,
        getScrollElement: () => scroller.current,
        estimateSize: () => 38,
        overscan: 12,
    })

    // The latest transfer of each recording on this TiVo, by recording.
    const transferOf = useMemo(() => {
        const m = new Map<string, Transfer>()
        for (const t of transfers) if (t.tsn === tsn) m.set(t.recording.id, t)
        return m
    }, [transfers, tsn])

    const download = async (recs: Recording[]) => {
        const ids = recs.filter(r => !blocked(r)).map(r => r.id)
        try {
            setError('')
            setNotice('')
            const n = await api.queueDownloads(tsn, ids)
            const skipped = recs.length - n
            setNotice(`${plural(n, 'recording', 'recordings')} added to Transfers`
                + (skipped > 0 ? `; ${skipped} left out (protected, still recording or already waiting).` : '.'))
        } catch (e) {
            setError(errorText(e))
        }
    }

    const toggle = (title: string) => setOpen(prev => {
        const next = new Set(prev)
        if (next.has(title)) next.delete(title)
        else next.add(title)
        return next
    })

    if (tivos.length === 0) {
        return (
            <>
                <h1>Now Playing</h1>
                <section className="card empty">Looking for TiVos on your network…</section>
            </>
        )
    }

    const shownCount = rows.reduce((n, r) => n + (r.kind === 'recording' ? 1 : r.open ? 0 : r.items.length), 0)
    const totalBytes = data.recordings.reduce((n, r) => n + r.size, 0)
    const pct = data.total > 0 ? Math.round(100 * data.loaded / data.total) : 0

    return (
        <div className="np-page wide">
            <div className="heading">
                <h1>Now Playing</h1>
                <div className="row">
                    <label htmlFor="np-tivo" className="sr-only">TiVo</label>
                    <select id="np-tivo" value={tsn} onChange={e => setTSN(e.target.value)}>
                        {tivos.map(t => (
                            <option key={t.tsn} value={t.tsn}>{t.name}{t.online ? '' : ' (offline)'}</option>
                        ))}
                    </select>
                    <button disabled={data.loading || !hasMAK} onClick={() => refresh(tsn)}>
                        {data.loading ? 'Loading…' : 'Refresh'}
                    </button>
                </div>
            </div>

            {!hasMAK && (
                <section className="card">
                    <h2>Media Access Key needed</h2>
                    <p>A TiVo only lists its recordings for someone who has its Media Access Key.</p>
                    <button className="primary" onClick={() => go('settings')}>Enter it in Settings</button>
                </section>
            )}
            {(error || data.error) && <p className="error" role="alert">{error || data.error}</p>}
            {notice && (
                <p className="good" role="status">
                    {notice} <button className="link" onClick={() => go('transfers')}>Show Transfers</button>
                </p>
            )}

            {data.loading && (
                <div className="progress" role="progressbar" aria-valuemin={0} aria-valuemax={100} aria-valuenow={pct}
                     aria-label="Loading recordings">
                    <div className="bar" style={{width: pct + '%'}}/>
                    <span>{data.total > 0 ? `${data.loaded} of ${data.total}` : 'Asking the TiVo…'}</span>
                </div>
            )}

            <div className="row np-tools">
                <label htmlFor="np-search" className="sr-only">Search recordings</label>
                <input id="np-search" className="grow" type="search" placeholder="Search titles and descriptions"
                       value={query} onChange={e => setQuery(e.target.value)}/>
                <label htmlFor="np-sort" className="sr-only">Sort</label>
                <select id="np-sort" value={sort} onChange={e => setSort(e.target.value as Sort)}>
                    <option value="recent">Newest first</option>
                    <option value="title">By title</option>
                </select>
            </div>

            <div className="np-body">
                <div className="np-list" ref={scroller}>
                    <div style={{height: virtual.getTotalSize(), position: 'relative'}}>
                        {virtual.getVirtualItems().map(v => {
                            const row = rows[v.index]
                            const style = {transform: `translateY(${v.start}px)`, height: v.size}
                            if (row.kind === 'group') {
                                return (
                                    <div key={row.key} className="np-row group" style={style}>
                                        <button className="np-main" aria-expanded={row.open}
                                                onClick={() => toggle(row.title)}>
                                            <span className="chev">{row.open ? '▾' : '▸'}</span>
                                            <span className="np-title">{row.title}</span>
                                            <span className="count">{row.items.length}</span>
                                        </button>
                                        <button className="np-get" disabled={!hasMAK}
                                                title={`Download all ${row.items.length}`}
                                                aria-label={`Download all ${row.items.length} recordings of ${row.title}`}
                                                onClick={() => download(row.items)}>↓ All</button>
                                        <span className="np-date">{shortDate(row.newest)}</span>
                                        <span className="np-size">{size(row.bytes)}</span>
                                    </div>
                                )
                            }
                            const r = row.rec
                            return (
                                <button key={row.key} style={style}
                                        className={'np-row' + (row.nested ? ' nested' : '') + (selected?.id === r.id ? ' selected' : '')}
                                        onClick={() => setSelected(r)}>
                                    <span className="chev"/>
                                    <span className="np-title">
                                        {row.nested ? (r.episodeTitle || r.title) : r.title}
                                        {!row.nested && r.episodeTitle && <span className="muted"> · {r.episodeTitle}</span>}
                                    </span>
                                    {r.status !== 'normal' && <span className={'badge ' + r.status}>{statusLabel[r.status]}</span>}
                                    {transferOf.get(r.id) && transferMark[transferOf.get(r.id)!.state] && (
                                        <span className={'mark ' + transferOf.get(r.id)!.state}
                                              title={'Transfer: ' + transferOf.get(r.id)!.state}>
                                            {transferMark[transferOf.get(r.id)!.state]}
                                        </span>
                                    )}
                                    <span className="np-date">{shortDate(r.captured)}</span>
                                    <span className="np-size">{size(r.size)}</span>
                                </button>
                            )
                        })}
                    </div>
                    {rows.length === 0 && !data.loading && (
                        <p className="muted np-none">
                            {data.recordings.length === 0 ? 'No recordings loaded.' : 'Nothing matches your search.'}
                        </p>
                    )}
                </div>

                {selected && (
                    <aside className="np-details" aria-label="Recording details">
                        <div className="heading">
                            <h2>{selected.title}</h2>
                            <button aria-label="Close details" onClick={() => setSelected(null)}>✕</button>
                        </div>
                        {selected.episodeTitle && <p><strong>{selected.episodeTitle}</strong></p>}
                        {selected.status !== 'normal' &&
                            <p><span className={'badge ' + selected.status}>{statusLabel[selected.status]}</span></p>}
                        <p>{selected.description || <span className="muted">No description.</span>}</p>
                        <dl className="facts">
                            <dt>Recorded</dt>
                            <dd>{longDate(selected.captured)}</dd>
                            <dt>Length</dt>
                            <dd>{duration(selected.durationMs)}</dd>
                            <dt>Size</dt>
                            <dd>{size(selected.size)}{selected.hd ? ' · HD' : ''}</dd>
                            {selected.station && <>
                                <dt>Channel</dt>
                                <dd>{selected.channel} {selected.station}</dd>
                            </>}
                            {selected.episodeNumber && <>
                                <dt>Episode</dt>
                                <dd>{selected.episodeNumber}</dd>
                            </>}
                        </dl>
                        <DownloadBox rec={selected} transfer={transferOf.get(selected.id)} canAsk={hasMAK}
                                     onDownload={() => download([selected])} onShow={() => go('transfers')}/>
                    </aside>
                )}
            </div>

            <div className="np-foot muted">
                {plural(data.recordings.length, 'recording', 'recordings')}
                {query && ` · ${shownCount.toLocaleString()} shown`}
                {' · '}{size(totalBytes)}
                {' · '}checked {ago(data.fetched)}{data.unchanged ? ' (no changes)' : ''}
            </div>
        </div>
    )
}

// The download button for one recording, with where its transfer stands.
function DownloadBox({rec, transfer, canAsk, onDownload, onShow}: {
    rec: Recording
    transfer?: Transfer
    canAsk: boolean
    onDownload: () => void
    onShow: () => void
}) {
    const why = blocked(rec)
    if (why) return <p className="muted">{why}</p>

    const busy = transfer?.state === 'queued' || transfer?.state === 'running'
    return (
        <div className="np-download">
            {transfer?.state === 'queued' && <p className="muted">Waiting in the download queue.</p>}
            {transfer?.state === 'running' && <p className="muted">Downloading… {percent(transfer)}%</p>}
            {transfer?.state === 'done' && <p className="good">Downloaded.</p>}
            {transfer?.state === 'failed' && <p className="error">The last download failed: {transfer.error}</p>}
            <div className="row">
                {!busy && (
                    <button className="primary" disabled={!canAsk} onClick={onDownload}>
                        {transfer?.state === 'done' ? 'Download again' : 'Download'}
                    </button>
                )}
                {transfer && <button onClick={onShow}>Show Transfers</button>}
            </div>
        </div>
    )
}
