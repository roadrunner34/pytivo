> **Status:** reference design only. Nothing has been built yet. Drafted 2026-10-24 on branch `py3-modernization`.

# TivoDesk — a Go + Wails desktop replacement for pyTivo

## Context
pyTivo has been ported to Python 3 (`py3-modernization`), but its architecture is still slow. The main costs:
- It pages Now Playing synchronously, inside the request thread.
- Zeroconf startup blocks for 30 s.
- A TiVo's first request blocks for up to 5 s on `get_name`.
- 503 retries never give up.
- It runs an ffmpeg probe on every request.
- Python checks each TS packet one at a time and sleeps after every chunk.
- Decryption depends on tivolibre, which needs Java.

The user wants a new, modern app that **fully replaces pyTivo**. It should be fast, use the MAK, and share no code with pyTivo. pyTivo stays in the repo as **reference only**.

Decisions made:
- **Go + Wails v2** desktop app (WebView2), React + TS + Vite UI
- **Native .TiVo decryption (PS and TS) from day one**, with no Java or tivolibre at runtime
- **TiVo RPC (port 1413) in v1**: rich recording list, delete, To Do, season passes, remote keys. The client certificate is supplied by the user, not embedded.
- ffmpeg/ffprobe stay external binaries, bundled for transcoding

Location: new top-level folder `tivodesk/` (its own Go module, go 1.24+) on a new branch `tivodesk`.

## Architecture
All logic lives in a headless engine (`internal/core`). The Wails `app.go` is a thin layer of bindings plus an event bridge. `cmd/tivodesk-cli` runs the same engine for tests, service mode and debugging tools (`decrypt`, `npl`, `rpc`, `tlsprobe`, `golden`).

| Package | Responsibility | pyTivo reference |
|---|---|---|
| `config` (+`pytivoimport`) | TOML config; lookup order `_tivo_<TSN>` → SD/HD/4K class → Server; TSN classification; strtod/bitrate helpers; INI importer (cp1252 fallback) | `config.py` (get_tsn :472, is_ts_capable :364, strtod :484), `plugins/settings/help.txt` |
| `secrets` | MAK and cert password in Windows Credential Manager (go-keyring) | — |
| `discovery` | Continuous async mDNS browse of `_tivo-videos`; advertise shares; UDP/TCP 2190 beacon; async cached `get_name` | `beacon.py` |
| `tivo` / `netutil` | TiVo registry (TSN, IP, TXT props); interface selection (skip Hyper-V/WSL/VPN); per-subnet broadcast | `httpserver.py:146-164`, `config.py:170` |
| `hmo` (`video/music/photo`) | `net/http` TiVoConnect server: QueryContainer/Item/Formats/Server, TVBusQuery, paging/anchors/sort/recurse, Range/206, synthetic TiVo header; `text/template` XML parsed once | `httpserver.py`, `plugin.py:146-198`, `plugins/video/video.py`, `plugins/video/templates/*`, `templates/root_container.tmpl`, `plugins/music`, `plugins/photo` |
| `library` | SQLite index (modernc.org/sqlite), fsnotify watcher, ffprobe worker pool, so browsing never probes inline | — |
| `media/{probe,transcode,meta,image}` | ffprobe JSON normalized to pyTivo's vInfo; compatibility rules plus ffmpeg argument builder; ring buffer for resume; metadata formats (.txt/.nfo/MP4/WTV) | `plugins/video/transcode.py`, `metadata.py` |
| `tivocrypt/{turing,tivofile,ps,ts}` | Native Turing cipher; header and metadata decrypt; PS and TS stream decoders as streaming `io.Reader`s | `turing.py`, `metadata.py:748-797`; algorithm from tivolibre (BSD-2) / tivodecode |
| `togo` | Per-TiVo `http.Client`: legacy TLS, Digest/Basic (`tivo`/MAK), `sid` cookie, TSN header; NPL cached by LastChangeDate, pages streamed to UI; capped exponential backoff on 503 | `plugins/togo/togo.py` (tivo_open :144, NPL :189, get_tivo_file :646, naming :545-643) |
| `jobs` | Download queue persisted in SQLite; one worker per TiVo; pipeline download → decrypt → TS validation → write → `.txt` details → post-process hook; prevent sleep | `togo.py:1009-1057, :733-764, :852-864` |
| `rpc` | MRPC/2 client over TLS with client cert (.p12 via go-pkcs12, or PEM); request multiplexing by RpcId; keepalive/reconnect | kmttg `rpc/Remote.java` (external) |
| `events`, `logx`, `power`, `winsvc` | Pub/sub to UI (progress throttled to ~4 Hz); slog plus a ring buffer for the log viewer with the MAK masked; SetThreadExecutionState; optional service mode and firewall helpers | — |

**Key libraries:**
- `brutella/dnssd` for mDNS
- `icholy/digest` or a small in-house digest implementation
- `go-toml/v2` and `ini.v1` for config and import
- `fsnotify` for folder watching
- `golang-lru/v2` for caches
- `dhowden/tag` for music tags
- `x/image` and `goexif` for photos
- `x/time/rate` for rate limiting
- `energye/systray` for the tray (spike in P0)
- Frontend: Zustand, TanStack Table + Virtual

**Legacy TLS (ToGo):**
- `InsecureSkipVerify` and `MinVersion: TLS10`.
- An explicit `CipherSuites` list that includes the RSA key-exchange and 3DES suites.
- `//go:debug tlsrsakex=1,tls3des=1,tlssha1=1` in go.mod's `godebug` block.
- `tlsprobe` is the first thing to run against the real TiVos. Fallback: HTTP on port 80 with digest auth.

**UI pages:**
- Dashboard: health, ports, TiVos online, active transfers, warnings
- TiVos
- Now Playing: virtualized, grouped by series; download/delete; details drawer
- Transfers: ToGo queue plus serving-to-TiVo
- To Do
- Season Passes
- Remote
- Shares
- Settings: includes the pyTivo.conf import wizard and the RPC cert
- Logs

The web admin pages (settings/togo/desktop/vrd plugins) are dropped; the desktop UI replaces them.

## Phases (each ends with a demo plus green CI: `go test ./...`, golangci-lint, `npm run build`, `wails build` on windows-latest)

| Phase | What gets built | Done when |
|---|---|---|
| **P0 Foundation** | Wails init, engine lifecycle, event bus, logging, config/TOML + importer, secrets, single instance, tray spike | App imports `D:\pytivo\pyTivo.conf` and edits settings; `tests/test_config.py` cases ported |
| **P1 Discovery + ToGo raw** | mDNS browse, togo client, NPL cache, jobs queue, raw PS/TS `.TiVo` download, naming, `.txt` details | 1,000+ recordings listed in seconds (instant with a warm cache); PS and TS downloads; **collect real decryption fixtures** |
| **P2 Native decryption** | turing → tivofile/metadata → PS → TS; streaming decrypt during download; `tivodesk-cli decrypt` | SHA-256 of output equals `bin\tivolibre.exe` output for every fixture; Turing at least 500 MB/s |
| **P3 HMO video, no transcoding** | hmo server, library index, video container/TVBus/paging, send_file with Range, `.TiVo` passthrough plus decrypt-on-serve, mDNS advertising, beacon | Share appears on the TiVo with the correct icon; 5k-file browse instant; MPEG-2 plays and resumes; golden XML matches pyTivo |
| **P4 Transcoding** | probe normalization, compatibility rules, ffmpeg argument builder, chunked streaming, resume ring buffer, `.TiVo` → native decrypt → ffmpeg | MKV/MP4 plays on SD (PS) and on Roamio/Bolt (TS copy); golden ffmpeg arguments match pyTivo |
| **P5 Music + photo** | playlists, shuffle, seek; scale, rotate, EXIF | Plays and displays correctly on a TiVo |
| **P6 RPC** (parallel track after P1) | MRPC framing/auth/session; recordings, delete, To Do, season passes, keyEventSend; capability detection | All operations work against a real TiVo plus the fake MRPC server; unsupported TiVos degrade gracefully |
| **P7 Packaging** | NSIS installer with firewall rules (TCP 9032, TCP/UDP 2190, UDP 5353), start at login, headless service, bundled ffmpeg, detection of a running pyTivo / port conflicts, pprof pass, signing | Clean install on Win11; performance targets met |

Performance targets: container browse under 50 ms server time for 5k entries; warm NPL under 200 ms; cold start to advertising under 1 s.

## Main risks
1. **TS decryption is the hardest piece.** The private TiVo PID (stream_type 0x97) carries per-stream key blocks, PES headers can be split across packets, and key packets can arrive late, so scrambled packets must be buffered. Mitigation: build PS first, port tivolibre's structure, gate on byte-for-byte equality, fuzz the parsers, and resync on 0x47.
2. **Legacy TLS** on older TiVos (see the TLS settings above; run `tlsprobe` in week one of P1).
3. **RPC client cert.** The user supplies it; RPC is behind a feature flag and is optional for the rest of the app.
4. **Coexistence with a running pyTivo.** Ports 9032/2190 may be taken; use a distinct, persistent GUID (pyTivo regenerates its GUID on every start).
5. **Multi-homed Windows and the firewall.** Add an interface-selection setting; the installer adds firewall rules and the Dashboard offers a fix.
6. **Probe drift** between ffprobe and pyTivo's `ffmpeg -i` parsing changes compatibility decisions. Guard with golden tests over a media matrix.
7. **Licensing.** Turing/Qualcomm attribution goes in the About page; ship ffmpeg's license notices.

## Verification
- **Unit tests:** port `tests/test_turing.py`, `test_config.py` and `test_metadata.py`; add tests for naming, NPL parsing, digest auth (`httptest`), MRPC framing and paging rules; fuzz the PS/TS/header/MRPC/Range parsers.
- **Golden parity with pyTivo:**
  - A harness in `tivodesk/tools/golden/` reuses `tests/test_server.py` to run pyTivo, with a pinned GUID, on lavfi-generated fixtures.
  - It records the XML for a matrix of commands × TSN classes (SD `540`, HD `648`, TS `746`/`663`, 4K `849`).
  - It also records the `transcode()` arguments and `tivo_compatible()` result as JSON, and `tivo_header()` bytes.
  - The Go tests compare canonicalized output against these files.
- **Decryption golden:** real `.TiVo` fixtures kept outside the repo (pointed to by `TIVODESK_FIXTURES`); compare against tivolibre/tdcat output; tests skip when fixtures are absent; benchmarks for Turing/PS/TS.
- **Fakes:** a fake TiVo (TLS, digest, sid cookie, NPL pages, injected 503s, downloads); a fake MRPC server that requires a client cert; loopback mDNS/beacon.
- **Manual end-to-end against the user's two LAN TiVos, per release candidate:**
  - Discovery within 5 s
  - Browse and paging; jump by letter
  - Playback and resume; stop kills ffmpeg
  - Transcode on SD, HD and 4K
  - Music and photos
  - ToGo PS/TS raw and decrypted, compared with tivolibre; NPL refresh time vs pyTivo
  - The download queue survives a restart
  - RPC: delete, To Do, season passes, remote latency under 150 ms
  - Port-conflict banner; MAK masked in logs
