import {useState} from 'react'
import {AppState} from '../App'
import {api, Config, errorText} from '../api'

const types = ['video', 'music', 'photo'] as const

interface Draft {
    original: string | null // the share being edited, or null for a new one
    name: string
    type: string
    path: string
}

export default function Shares({config, save}: AppState) {
    const [draft, setDraft] = useState<Draft | null>(null)
    const [error, setError] = useState('')
    const names = Object.keys(config.share).sort((a, b) => a.localeCompare(b))

    const commit = async (next: Config) => {
        try {
            await save(next)
            setDraft(null)
            setError('')
        } catch (e) {
            setError(errorText(e))
        }
    }

    const saveDraft = async () => {
        if (!draft) return
        const name = draft.name.trim()
        if (!name) return setError('Give the share a name. It is what the TiVo shows.')
        if (!draft.path.trim()) return setError('Choose the folder this share serves.')
        if (name !== draft.original && config.share[name]) return setError(`There is already a share called “${name}”.`)

        const share = {...config.share}
        // Keep any extra settings the share already had.
        const previous = draft.original ? share[draft.original] : {}
        if (draft.original) delete share[draft.original]
        share[name] = {...previous, type: draft.type, path: draft.path.trim()}
        await commit({...config, share})
    }

    const remove = async (name: string) => {
        const share = {...config.share}
        delete share[name]
        await commit({...config, share})
    }

    const browse = async () => {
        try {
            const folder = await api.chooseFolder('Choose the folder to share')
            if (folder && draft) setDraft({...draft, path: folder})
        } catch (e) {
            setError(errorText(e))
        }
    }

    return (
        <>
            <div className="heading">
                <h1>Shares</h1>
                {!draft && (
                    <button className="primary"
                            onClick={() => setDraft({original: null, name: '', type: 'video', path: ''})}>
                        Add a share
                    </button>
                )}
            </div>
            <p className="muted">A share is a folder that appears on your TiVos under its name.</p>

            {error && <p className="error" role="alert">{error}</p>}

            {draft && (
                <section className="card">
                    <h2>{draft.original ? `Edit “${draft.original}”` : 'New share'}</h2>
                    <div className="form">
                        <label htmlFor="share-name">Name</label>
                        <input id="share-name" value={draft.name} autoFocus
                               onChange={e => setDraft({...draft, name: e.target.value})}/>

                        <label htmlFor="share-type">Type</label>
                        <select id="share-type" value={draft.type}
                                onChange={e => setDraft({...draft, type: e.target.value})}>
                            {types.map(t => <option key={t} value={t}>{t}</option>)}
                        </select>

                        <label htmlFor="share-path">Folder</label>
                        <div className="row">
                            <input id="share-path" className="grow" value={draft.path}
                                   onChange={e => setDraft({...draft, path: e.target.value})}/>
                            <button onClick={browse}>Browse…</button>
                        </div>
                    </div>
                    <div className="row end">
                        <button onClick={() => {
                            setDraft(null)
                            setError('')
                        }}>Cancel
                        </button>
                        <button className="primary" onClick={saveDraft}>Save</button>
                    </div>
                </section>
            )}

            {names.length === 0 && !draft && (
                <section className="card empty">No shares yet. Add one, or import your pyTivo settings.</section>
            )}

            {names.length > 0 && (
                <table className="list">
                    <thead>
                    <tr>
                        <th>Name</th>
                        <th>Type</th>
                        <th>Folder</th>
                        <th><span className="sr-only">Actions</span></th>
                    </tr>
                    </thead>
                    <tbody>
                    {names.map(name => {
                        const s = config.share[name]
                        return (
                            <tr key={name}>
                                <td>{name}</td>
                                <td><span className={'tag ' + s.type}>{s.type}</span></td>
                                <td className="path">{s.path || <span className="warn">no folder set</span>}</td>
                                <td className="actions">
                                    <button onClick={() => {
                                        setError('')
                                        setDraft({original: name, name, type: s.type, path: s.path ?? ''})
                                    }}>Edit
                                    </button>
                                    <button className="danger" onClick={() => remove(name)}>Remove</button>
                                </td>
                            </tr>
                        )
                    })}
                    </tbody>
                </table>
            )}
        </>
    )
}
