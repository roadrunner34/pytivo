import {useCallback, useEffect, useState} from 'react'
import {api, Config, errorText, Info, on, Problem, TiVo, Topic, Transfer, TransferProgress} from './api'
import Dashboard from './pages/Dashboard'
import TiVos from './pages/TiVos'
import NowPlayingPage from './pages/NowPlaying'
import Transfers from './pages/Transfers'
import Shares from './pages/Shares'
import SettingsPage from './pages/Settings'
import Logs from './pages/Logs'

export type PageName = 'dashboard' | 'tivos' | 'nowplaying' | 'transfers' | 'shares' | 'settings' | 'logs'

const pages: { name: PageName; label: string }[] = [
    {name: 'dashboard', label: 'Dashboard'},
    {name: 'tivos', label: 'TiVos'},
    {name: 'nowplaying', label: 'Now Playing'},
    {name: 'transfers', label: 'Transfers'},
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
    tivos: TiVo[]
    // The TiVo whose recordings Now Playing shows.
    tsn: string
    setTSN: (tsn: string) => void
    // Every download, oldest first, kept current as they progress.
    transfers: Transfer[]
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
    const [tivos, setTivos] = useState<TiVo[]>([])
    const [tsn, setTSN] = useState('')
    const [transfers, setTransfers] = useState<Transfer[]>([])

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
        const loadTivos = () => api.tivos().then(setTivos).catch(e => setFailure(errorText(e)))
        loadTivos()
        const offTivos = on(Topic.tivosChanged, loadTivos)
        const loadTransfers = () => api.transfers().then(setTransfers).catch(e => setFailure(errorText(e)))
        loadTransfers()
        const offTransfers = on(Topic.transfersChanged, loadTransfers)
        // Progress arrives twice a second; patch the one row it is about.
        const offProgress = on<TransferProgress>(Topic.transferProgress, p => {
            setTransfers(list => list.map(t => t.id === p.id ? {...t, ...p} : t))
        })
        return () => {
            offTransfers()
            offProgress()
            offTivos()
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

    // Default to the first TiVo that is online once any are known.
    const current = tivos.find(t => t.tsn === tsn) ?? tivos.find(t => t.online) ?? tivos[0]
    const state: AppState = {
        config, hasMAK, info, problems, save, go: setPage,
        tivos, tsn: current?.tsn ?? '', setTSN, transfers,
    }
    const active = transfers.filter(t => t.state === 'queued' || t.state === 'running').length
    return (
        <div className="shell">
            <nav className="sidebar">
                <div className="brand">TivoDesk</div>
                {pages.map(p => (
                    <button key={p.name} className={'nav' + (p.name === page ? ' active' : '')}
                            aria-current={p.name === page ? 'page' : undefined}
                            onClick={() => setPage(p.name)}>
                        {p.label}
                        {p.name === 'transfers' && active > 0 &&
                            <span className="count" aria-label={`${active} in progress`}>{active}</span>}
                    </button>
                ))}
                <div className="version">v{info.version}</div>
            </nav>
            <main className="content">
                {page === 'dashboard' && <Dashboard {...state}/>}
                {page === 'tivos' && <TiVos {...state}/>}
                {page === 'nowplaying' && <NowPlayingPage {...state}/>}
                {page === 'transfers' && <Transfers {...state}/>}
                {page === 'shares' && <Shares {...state}/>}
                {page === 'settings' && <SettingsPage {...state}/>}
                {page === 'logs' && <Logs/>}
            </main>
        </div>
    )
}
