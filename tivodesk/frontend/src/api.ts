// The one place the UI talks to the Go engine. Everything else imports from
// here, so the generated bindings can change without touching the pages.
import * as Go from '../wailsjs/go/main/App'
import {EventsOn} from '../wailsjs/runtime/runtime'

export type Settings = Record<string, string>

export interface Config {
    server: Settings
    class: Record<string, Settings>
    tivo: Record<string, Settings>
    share: Record<string, Settings>
}

export interface Problem {
    section: string
    name: string
    message: string
}

export interface Info {
    version: string
    goVersion: string
    configPath: string
}

export interface ImportPreview {
    config: Config
    hasMAK: boolean
    tivoMAKs: number
    warnings: string[]
}

export interface LogEntry {
    seq: number
    time: string
    level: 'DEBUG' | 'INFO' | 'WARN' | 'ERROR'
    message: string
}

export interface TiVo {
    tsn: string
    name: string
    address: string
    port: number
    platform: string
    swVersion: string
    online: boolean
    lastSeen: string
}

export interface Recording {
    id: string
    title: string
    episodeTitle: string
    description: string
    seriesId: string
    programId: string
    episodeNumber: string
    captured: string
    durationMs: number
    size: number
    channel: string
    station: string
    hd: boolean
    inProgress: boolean
    copyProtected: boolean
    status: 'normal' | 'protected' | 'expiring' | 'expired' | 'kuid' | 'suggestion' | 'inprogress'
    tvRating: string
    mpaaRating: string
    contentUrl: string
    detailsUrl: string
}

export interface NowPlayingStatus {
    tsn: string
    loading: boolean
    loaded: number
    total: number
    error: string
    fetched: string
    unchanged: boolean
}

export interface NowPlaying extends NowPlayingStatus {
    recordings: Recording[]
}

export interface Transfer {
    id: number
    tsn: string
    tivoName: string
    recording: Recording
    ts: boolean
    state: 'queued' | 'running' | 'done' | 'failed' | 'cancelled'
    error: string
    path: string
    bytes: number
    syncLosses: number
    attempts: number
    created: string
    started: string
    finished: string
    rate: number
}

export interface TransferProgress {
    id: number
    bytes: number
    rate: number
    syncLosses: number
}

// Topics the engine publishes; they match internal/events.
export const Topic = {
    configChanged: 'config.changed',
    makChanged: 'mak.changed',
    logEntry: 'log.entry',
    tivosChanged: 'tivos.changed',
    nowPlayingChanged: 'nowplaying.changed',
    transfersChanged: 'transfers.changed',
    transferProgress: 'transfer.progress',
} as const

// The generated bindings type maps loosely, so results are cast to the
// interfaces above. Go fills every map, so none of them is ever null.
export const api = {
    info: () => Go.GetInfo() as Promise<Info>,
    config: () => Go.GetConfig() as unknown as Promise<Config>,
    saveConfig: (c: Config) => Go.SaveConfig(c as never) as unknown as Promise<Problem[]>,
    problems: () => Go.GetProblems() as unknown as Promise<Problem[]>,
    hasMAK: () => Go.HasMAK(),
    setMAK: (mak: string) => Go.SetMAK(mak),
    clearMAK: () => Go.ClearMAK(),
    previewImport: (path: string) => Go.PreviewImport(path) as unknown as Promise<ImportPreview>,
    applyImport: (path: string) => Go.ApplyImport(path) as unknown as Promise<ImportPreview>,
    tivos: () => Go.GetTiVos() as unknown as Promise<TiVo[]>,
    nowPlaying: (tsn: string) => Go.GetNowPlaying(tsn) as unknown as Promise<NowPlaying>,
    refreshNowPlaying: (tsn: string) => Go.RefreshNowPlaying(tsn),
    transfers: () => Go.GetTransfers() as unknown as Promise<Transfer[]>,
    queueDownloads: (tsn: string, ids: string[]) => Go.QueueDownloads(tsn, ids),
    cancelTransfer: (id: number) => Go.CancelTransfer(id),
    retryTransfer: (id: number) => Go.RetryTransfer(id),
    clearTransfers: () => Go.ClearTransfers(),
    showTransfer: (id: number) => Go.ShowTransfer(id),
    logs: () => Go.GetLogs() as unknown as Promise<LogEntry[]>,
    chooseImportFile: () => Go.ChooseImportFile(),
    chooseFolder: (title: string) => Go.ChooseFolder(title),
    chooseFile: (title: string) => Go.ChooseFile(title),
}

// on subscribes to an engine event and returns the function that
// unsubscribes, which is the shape React effects want.
export function on<T = unknown>(topic: string, fn: (data: T) => void): () => void {
    return EventsOn(topic, fn)
}

// Go errors arrive as strings or Error objects, depending on the call.
export function errorText(e: unknown): string {
    if (e instanceof Error) return e.message
    return String(e)
}
