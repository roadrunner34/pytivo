import {useEffect, useState} from 'react'
import {AppState} from '../App'
import {api, errorText, ImportPreview} from '../api'

export default function SettingsPage(state: AppState) {
    return (
        <>
            <h1>Settings</h1>
            <MediaAccessKey {...state}/>
            <Server {...state}/>
            <ImportPyTivo/>
        </>
    )
}

function MediaAccessKey({hasMAK}: AppState) {
    const [mak, setMak] = useState('')
    const [error, setError] = useState('')

    const store = async () => {
        try {
            await api.setMAK(mak.trim())
            setMak('')
            setError('')
        } catch (e) {
            setError(errorText(e))
        }
    }
    const clear = async () => {
        try {
            await api.clearMAK()
            setError('')
        } catch (e) {
            setError(errorText(e))
        }
    }

    return (
        <section className="card">
            <h2>Media Access Key</h2>
            <p className="muted">
                The 10-digit key for your TiVo account. It is needed to download and decrypt recordings. You can
                find it on a TiVo under Settings → Account &amp; System Info. TivoDesk keeps it in Windows
                Credential Manager, not in the settings file, and never writes it to the log.
            </p>
            <p>Status: <strong className={hasMAK ? 'good' : 'warn'}>{hasMAK ? 'Stored' : 'Not set'}</strong></p>
            <div className="row">
                <label htmlFor="mak" className="sr-only">Media Access Key</label>
                <input id="mak" type="password" inputMode="numeric" maxLength={10} autoComplete="off"
                       placeholder={hasMAK ? 'Enter a new key to replace it' : '10 digits'}
                       value={mak} onChange={e => setMak(e.target.value)}
                       onKeyDown={e => e.key === 'Enter' && mak && store()}/>
                <button className="primary" disabled={!mak} onClick={store}>{hasMAK ? 'Replace' : 'Save'}</button>
                {hasMAK && <button className="danger" onClick={clear}>Remove</button>}
            </div>
            {error && <p className="error" role="alert">{error}</p>}
        </section>
    )
}

function Server({config, save}: AppState) {
    const [port, setPort] = useState('')
    const [ffmpeg, setFfmpeg] = useState('')
    const [togo, setTogo] = useState('')
    const [debug, setDebug] = useState(false)
    const [status, setStatus] = useState<{ ok: boolean; text: string } | null>(null)

    // Reset the form whenever the stored settings change, such as after an
    // import.
    useEffect(() => {
        setPort(config.server.port ?? '')
        setFfmpeg(config.server.ffmpeg ?? '')
        setTogo(config.server.togo_path ?? '')
        setDebug(['1', 'yes', 'true', 'on'].includes((config.server.debug ?? '').toLowerCase()))
    }, [config])

    const pick = async (kind: 'file' | 'folder', title: string, set: (v: string) => void) => {
        try {
            const chosen = kind === 'file' ? await api.chooseFile(title) : await api.chooseFolder(title)
            if (chosen) set(chosen)
        } catch (e) {
            setStatus({ok: false, text: errorText(e)})
        }
    }

    const submit = async () => {
        const p = port.trim()
        if (p && !(/^\d+$/.test(p) && +p >= 1 && +p <= 65535)) {
            return setStatus({ok: false, text: 'The port must be a number from 1 to 65535.'})
        }
        // Start from every existing setting so the ones not shown survive.
        const server = {...config.server}
        const put = (key: string, value: string) => {
            if (value.trim()) server[key] = value.trim()
            else delete server[key]
        }
        put('port', p)
        put('ffmpeg', ffmpeg)
        put('togo_path', togo)
        put('debug', debug ? 'true' : '')
        try {
            await save({...config, server})
            setStatus({ok: true, text: 'Saved.'})
        } catch (e) {
            setStatus({ok: false, text: errorText(e)})
        }
    }

    return (
        <section className="card">
            <h2>Server</h2>
            <div className="form">
                <label htmlFor="port">Port</label>
                <input id="port" className="short" inputMode="numeric" placeholder="9032"
                       value={port} onChange={e => setPort(e.target.value)}/>

                <label htmlFor="ffmpeg">ffmpeg</label>
                <div className="row">
                    <input id="ffmpeg" className="grow" placeholder="Path to ffmpeg.exe"
                           value={ffmpeg} onChange={e => setFfmpeg(e.target.value)}/>
                    <button onClick={() => pick('file', 'Choose ffmpeg.exe', setFfmpeg)}>Browse…</button>
                </div>

                <label htmlFor="togo">Recordings folder</label>
                <div className="row">
                    <input id="togo" className="grow" placeholder="Where downloaded recordings are saved"
                           value={togo} onChange={e => setTogo(e.target.value)}/>
                    <button onClick={() => pick('folder', 'Choose where recordings are saved', setTogo)}>Browse…</button>
                </div>

                <span/>
                <label className="check">
                    <input type="checkbox" checked={debug} onChange={e => setDebug(e.target.checked)}/>
                    Detailed logging
                </label>
            </div>
            <div className="row end">
                {status && <span className={status.ok ? 'good' : 'error'} role="status">{status.text}</span>}
                <button className="primary" onClick={submit}>Save</button>
            </div>
        </section>
    )
}

function ImportPyTivo() {
    const [path, setPath] = useState('')
    const [preview, setPreview] = useState<ImportPreview | null>(null)
    const [done, setDone] = useState(false)
    const [error, setError] = useState('')

    const choose = async () => {
        try {
            const chosen = await api.chooseImportFile()
            if (!chosen) return
            setPath(chosen)
            setDone(false)
            setError('')
            setPreview(await api.previewImport(chosen))
        } catch (e) {
            setPreview(null)
            setError(errorText(e))
        }
    }

    const apply = async () => {
        try {
            setPreview(await api.applyImport(path))
            setDone(true)
            setError('')
        } catch (e) {
            setError(errorText(e))
        }
    }

    const count = (n: number, one: string, many: string) => `${n} ${n === 1 ? one : many}`

    return (
        <section className="card">
            <h2>Import from pyTivo</h2>
            <p className="muted">
                Reads a <code>pyTivo.conf</code> and brings across its shares, server settings, per-TiVo settings
                and Media Access Key. You see what will be imported before anything changes.
            </p>
            <button onClick={choose}>Choose pyTivo.conf…</button>

            {error && <p className="error" role="alert">{error}</p>}

            {preview && (
                <div className="preview">
                    <p><code>{path}</code></p>
                    <ul className="plain">
                        <li>{count(Object.keys(preview.config.share).length, 'share', 'shares')}</li>
                        <li>{count(Object.keys(preview.config.server).length, 'server setting', 'server settings')}</li>
                        <li>{count(Object.keys(preview.config.tivo).length, 'TiVo with its own settings', 'TiVos with their own settings')}</li>
                        <li>Media Access Key: {preview.hasMAK ? 'found' : 'none in the file'}</li>
                    </ul>
                    {preview.warnings.length > 0 && (
                        <>
                            <h3>Notes</h3>
                            <ul className="notes">
                                {preview.warnings.map((w, i) => <li key={i}>{w}</li>)}
                            </ul>
                        </>
                    )}
                    {done
                        ? <p className="good" role="status">Imported. Your settings now match this file.</p>
                        : (
                            <div className="row">
                                <button className="primary" onClick={apply}>Import and replace current settings</button>
                                <button onClick={() => setPreview(null)}>Cancel</button>
                            </div>
                        )}
                </div>
            )}
        </section>
    )
}
