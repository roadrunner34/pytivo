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

// Topics the engine publishes; they match internal/events.
export const Topic = {
    configChanged: 'config.changed',
    makChanged: 'mak.changed',
    logEntry: 'log.entry',
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
