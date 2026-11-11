import {useCallback, useEffect, useState} from 'react'
import {api, Config, errorText, Info, on, Problem, Topic} from './api'
import Dashboard from './pages/Dashboard'
import Shares from './pages/Shares'
import SettingsPage from './pages/Settings'
import Logs from './pages/Logs'

export type PageName = 'dashboard' | 'shares' | 'settings' | 'logs'

const pages: { name: PageName; label: string }[] = [
    {name: 'dashboard', label: 'Dashboard'},
    {name: 'shares', label: 'Shares'},
    {name: 'settings', label: 'Settings'},
    {name: 'logs', label: 'Logs'},
]

// State every page needs, loaded once and kept current by engine events.
export interface AppState {
    config: Config
    hasMAK: boolean
    info: Info
    problems: Problem[]
    // save stores a changed configuration; it throws if the save fails.
    save: (c: Config) => Promise<void>
    go: (page: PageName) => void
}

export default function App() {
    const [page, setPage] = useState<PageName>('dashboard')
    const [config, setConfig] = useState<Config | null>(null)
    const [hasMAK, setHasMAK] = useState(false)
    const [info, setInfo] = useState<Info | null>(null)
    const [problems, setProblems] = useState<Problem[]>([])
    const [failure, setFailure] = useState('')

    const reload = useCallback(async () => {
        try {
            const [c, mak, p] = await Promise.all([api.config(), api.hasMAK(), api.problems()])
            setConfig(c)
            setHasMAK(mak)
            setProblems(p)
        } catch (e) {
            setFailure(errorText(e))
        }
    }, [])

    useEffect(() => {
        api.info().then(setInfo).catch(e => setFailure(errorText(e)))
        reload()
        const offConfig = on(Topic.configChanged, reload)
        const offMAK = on<boolean>(Topic.makChanged, setHasMAK)
        return () => {
            offConfig()
            offMAK()
        }
    }, [reload])

    const save = useCallback(async (c: Config) => {
        await api.saveConfig(c)
        // The engine emits config.changed, which reloads the config and
        // its problems.
    }, [])

    if (failure) {
        return <div className="center"><p className="error">TivoDesk could not load: {failure}</p></div>
    }
    if (!config || !info) {
        return <div className="center muted">Loading…</div>
    }

    const state: AppState = {config, hasMAK, info, problems, save, go: setPage}
    return (
        <div className="shell">
            <nav className="sidebar">
                <div className="brand">TivoDesk</div>
                {pages.map(p => (
                    <button key={p.name} className={'nav' + (p.name === page ? ' active' : '')}
                            aria-current={p.name === page ? 'page' : undefined}
                            onClick={() => setPage(p.name)}>
                        {p.label}
                    </button>
                ))}
                <div className="version">v{info.version}</div>
            </nav>
            <main className="content">
                {page === 'dashboard' && <Dashboard {...state}/>}
                {page === 'shares' && <Shares {...state}/>}
                {page === 'settings' && <SettingsPage {...state}/>}
                {page === 'logs' && <Logs/>}
            </main>
        </div>
    )
}
