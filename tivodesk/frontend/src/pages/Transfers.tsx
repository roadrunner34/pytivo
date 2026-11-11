import {useState} from 'react'
import {AppState} from '../App'
import {api, errorText, Transfer} from '../api'
import {plural, size} from '../format'

const stateLabel: Record<Transfer['state'], string> = {
    queued: 'Waiting',
    running: 'Downloading',
    done: 'Done',
    failed: 'Failed',
    cancelled: 'Cancelled',
}

// How far along a download is, from 0 to 100. The TiVo only estimates a
// recording's size, so a download under way never shows as complete.
export function percent(t: Transfer): number {
    if (t.state === 'done') return 100
    if (t.recording.size <= 0) return 0
    return Math.min(99, Math.floor(100 * t.bytes / t.recording.size))
}

function timeLeft(t: Transfer): string {
    if (t.rate <= 0 || t.recording.size <= t.bytes) return ''
    const s = (t.recording.size - t.bytes) / t.rate
    if (s < 90) return 'about a minute left'
    if (s < 3600) return `about ${Math.round(s / 60)} min left`
    return `about ${(s / 3600).toFixed(1)} h left`
}

export default function Transfers({transfers, tivos, config, go}: AppState) {
    const [error, setError] = useState('')

    const run = async (action: () => Promise<unknown>) => {
        try {
            setError('')
            await action()
        } catch (e) {
            setError(errorText(e))
        }
    }

    const active = transfers.filter(t => t.state === 'queued' || t.state === 'running')
    const finished = transfers.length - active.length
    const online = new Set(tivos.filter(t => t.online).map(t => t.tsn))
    // Newest first, but with unfinished downloads above the finished ones.
    const rows = [...active, ...transfers.filter(t => !active.includes(t)).reverse()]

    return (
        <div className="wide">
            <div className="heading">
                <h1>Transfers</h1>
                <button disabled={finished === 0} onClick={() => run(api.clearTransfers)}>Clear finished</button>
            </div>
            <p className="muted">
                Recordings being copied from your TiVos. Each TiVo sends one at a time; different TiVos download
                side by side. The queue is kept when TivoDesk closes.
                {config.server.togo_path && <> Files are saved in <code>{config.server.togo_path}</code>.</>}
            </p>
            {error && <p className="error" role="alert">{error}</p>}

            {transfers.length === 0 && (
                <section className="card empty">
                    <p>Nothing has been downloaded yet.</p>
                    <button onClick={() => go('nowplaying')}>Choose recordings in Now Playing</button>
                </section>
            )}

            {rows.length > 0 && (
                <table className="list transfers">
                    <thead>
                    <tr>
                        <th>Recording</th>
                        <th>Status</th>
                        <th><span className="sr-only">Actions</span></th>
                    </tr>
                    </thead>
                    <tbody>
                    {rows.map(t => {
                        const pct = percent(t)
                        const waitingForTiVo = t.state === 'queued' && !online.has(t.tsn)
                        return (
                            <tr key={t.id}>
                                <td>
                                    <div>
                                        {t.recording.title}
                                        {t.recording.episodeTitle && <span className="muted"> · {t.recording.episodeTitle}</span>}
                                    </div>
                                    <div className="sub plain">
                                        {t.tivoName} · {size(t.recording.size)} · {t.ts ? 'transport stream' : 'program stream'}
                                    </div>
                                </td>
                                <td className="status">
                                    {t.state === 'running' ? (
                                        <>
                                            <div className="progress small" role="progressbar" aria-valuemin={0}
                                                 aria-valuemax={100} aria-valuenow={pct}
                                                 aria-label={'Downloading ' + t.recording.title}>
                                                <div className="bar" style={{width: pct + '%'}}/>
                                                <span>{pct}%</span>
                                            </div>
                                            <div className="sub plain">
                                                {size(t.bytes)}
                                                {t.rate > 0 && ` · ${(t.rate / 1e6).toFixed(1)} MB/s`}
                                                {timeLeft(t) && ` · ${timeLeft(t)}`}
                                            </div>
                                        </>
                                    ) : (
                                        <>
                                            <div className={'state ' + t.state}>
                                                {waitingForTiVo ? `Waiting for ${t.tivoName} to come online` : stateLabel[t.state]}
                                                {t.state === 'done' && ` · ${size(t.bytes)}`}
                                            </div>
                                            {t.state === 'failed' && <div className="sub plain error">{t.error}</div>}
                                            {t.state === 'done' && t.syncLosses > 0 && (
                                                <div className="sub plain warn">
                                                    {plural(t.syncLosses, 'damaged packet', 'damaged packets')}; it may
                                                    glitch in places
                                                </div>
                                            )}
                                        </>
                                    )}
                                </td>
                                <td className="actions">
                                    {(t.state === 'queued' || t.state === 'running') &&
                                        <button onClick={() => run(() => api.cancelTransfer(t.id))}>Cancel</button>}
                                    {(t.state === 'failed' || t.state === 'cancelled') &&
                                        <button onClick={() => run(() => api.retryTransfer(t.id))}>Try again</button>}
                                    {t.state === 'done' &&
                                        <button onClick={() => run(() => api.showTransfer(t.id))}>Show in folder</button>}
                                </td>
                            </tr>
                        )
                    })}
                    </tbody>
                </table>
            )}
        </div>
    )
}
