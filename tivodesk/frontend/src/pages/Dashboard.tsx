import {AppState} from '../App'

export default function Dashboard({config, hasMAK, info, problems, tivos, go}: AppState) {
    const shares = Object.keys(config.share).length
    const online = tivos.filter(t => t.online).length
    const firstRun = shares === 0 && !hasMAK

    return (
        <>
            <h1>Dashboard</h1>

            {firstRun && (
                <section className="card callout">
                    <h2>Welcome to TivoDesk</h2>
                    <p>
                        If you already use pyTivo, import its settings and your shares and Media Access Key come
                        across in one step. Otherwise, add a share and enter your Media Access Key.
                    </p>
                    <div className="row">
                        <button className="primary" onClick={() => go('settings')}>Import from pyTivo</button>
                        <button onClick={() => go('shares')}>Add a share</button>
                    </div>
                </section>
            )}

            <div className="tiles">
                <button className="tile" onClick={() => go('shares')}>
                    <span className="tile-value">{shares}</span>
                    <span className="tile-label">{shares === 1 ? 'share' : 'shares'}</span>
                </button>
                <button className="tile" onClick={() => go('settings')}>
                    <span className={'tile-value ' + (hasMAK ? 'good' : 'warn')}>{hasMAK ? 'Stored' : 'Missing'}</span>
                    <span className="tile-label">Media Access Key</span>
                </button>
                <button className="tile" onClick={() => go('tivos')}>
                    <span className="tile-value">{online}</span>
                    <span className="tile-label">{online === 1 ? 'TiVo online' : 'TiVos online'}</span>
                </button>
            </div>

            {!hasMAK && !firstRun && (
                <section className="card">
                    <h2>No Media Access Key</h2>
                    <p>
                        Downloading and decrypting recordings needs your Media Access Key. You can find it on a
                        TiVo under Settings → Account &amp; System Info.
                    </p>
                    <button onClick={() => go('settings')}>Enter it in Settings</button>
                </section>
            )}

            {problems.length > 0 && (
                <section className="card">
                    <h2>Settings that will not work as written</h2>
                    <ul className="plain">
                        {problems.map(p => (
                            <li key={p.section + p.name}><code>{p.section}</code> · <code>{p.name}</code> {p.message}</li>
                        ))}
                    </ul>
                    <p className="muted">Each of these falls back to its default until it is fixed.</p>
                </section>
            )}

            <section className="card">
                <h2>About</h2>
                <dl className="facts">
                    <dt>Version</dt>
                    <dd>{info.version}</dd>
                    <dt>Settings file</dt>
                    <dd><code>{info.configPath}</code></dd>
                    <dt>Server port</dt>
                    <dd>{config.server.port || '9032'}</dd>
                </dl>
                <p className="muted">
                    This build finds your TiVos and lists their recordings. Downloads and serving media to TiVos
                    arrive in the next phases.
                </p>
            </section>
        </>
    )
}
