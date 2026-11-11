import {AppState} from '../App'
import {TiVo} from '../api'

const models: Record<string, string> = {
    'tcd/Series4': 'Premiere',
    'tcd/Series5': 'Roamio',
    'tcd/Series6': 'Bolt',
    'tcd/Keystone': 'Edge',
}

export function modelOf(t: TiVo): string {
    return models[t.platform] ?? t.platform.replace(/^tcd\//, '')
}

export default function TiVos({tivos, setTSN, go}: AppState) {
    return (
        <>
            <h1>TiVos</h1>
            <p className="muted">
                TiVos on your network that share their recordings. TivoDesk keeps looking, so a box that is
                switched on later appears here by itself.
            </p>

            {tivos.length === 0 && (
                <section className="card empty">
                    Looking for TiVos… If none appears within a minute, check that this PC and the TiVo are on
                    the same network, and that video sharing is enabled for the TiVo in your TiVo account.
                </section>
            )}

            {tivos.length > 0 && (
                <table className="list">
                    <thead>
                    <tr>
                        <th>Name</th>
                        <th>Model</th>
                        <th>Address</th>
                        <th>Software</th>
                        <th>Status</th>
                        <th><span className="sr-only">Actions</span></th>
                    </tr>
                    </thead>
                    <tbody>
                    {tivos.map(t => (
                        <tr key={t.tsn}>
                            <td>
                                <div>{t.name}</div>
                                <div className="sub">{t.tsn}</div>
                            </td>
                            <td>{modelOf(t)}</td>
                            <td className="mono">{t.address}</td>
                            <td className="mono">{t.swVersion.split('-')[0]}</td>
                            <td>
                                <span className={'dot ' + (t.online ? 'on' : 'off')}/>
                                {t.online ? 'Online' : 'Offline'}
                            </td>
                            <td className="actions">
                                <button disabled={!t.online} onClick={() => {
                                    setTSN(t.tsn)
                                    go('nowplaying')
                                }}>Recordings
                                </button>
                            </td>
                        </tr>
                    ))}
                    </tbody>
                </table>
            )}
        </>
    )
}
