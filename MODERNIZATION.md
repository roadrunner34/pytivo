# pyTivo Python 3 Modernization

This document records the Python 2 → Python 3 port of this fork: what
changed, why, how it was verified, and what to watch out for.

- **Branch:** `py3-modernization` (forked from `master` at `e7d56d3`)
- **Python:** 3.9+ required; tested on **3.11.9** and **3.13**
- **Status:** all modules import and run; 37 automated tests pass on both
  interpreters; tested against two real TiVos on the LAN (details in
  [Verification](#verification))
- **Size:** 88 files changed, +2,479 / −19,489 lines (most of the removed
  lines are vendored third-party libraries)

---

## 1. Where the earlier attempt left things

The three commits from February 2025 (`b267930`, `6771c52`, `e7d56d3`)
started the port, but the app could not run:

| Problem | Effect |
|---|---|
| `Cheetah/Parser.py` cut from 2,599 lines to 73 (its lexer and parser classes left as empty `pass` stubs) | No template could compile, so every page and every TiVo XML response failed |
| Five plugin modules still had Python 2 syntax (`print x`, `except X, e`, `lambda (k, v):`) | `music`, `photo`, `settings`, `togo` and `transcode` raised SyntaxError on import |
| Python 2 names throughout (`unicode`, `file()`, `xrange`, `cmp`, `reduce`, `urllib2`, `cookielib`, `thread`, `UserDict`) | NameError/ImportError at runtime |
| `config.get(section, name, raw)` passed `raw` positionally (it is keyword-only in Py3) | The TypeError was swallowed by a bare `except`, so **every per-TiVo setting silently returned `None`** |
| `/` now does float division | Bitrates became strings like `'448.0k'`, which ffmpeg rejects |
| Bytes and str mixed on sockets, files and subprocess pipes | Crashes on the first file transfer or beacon |
| The tray app's Mac path edits called `os.path.dirname(x, 'pyTivo.app')` | TypeError (`dirname` takes one argument) |
| `__pycache__` committed and then deleted | Harmless, now covered by `.gitignore` |

## 2. Dependencies: vendored code replaced with PyPI packages

| Removed from repo | Replaced by | Notes |
|---|---|---|
| `Cheetah/` (Cheetah 2.0, 2007, Py2-only, broken) | **Cheetah3 ≥ 3.2.6** | Same `.tmpl` syntax; templates are unchanged |
| `mutagen/` (1.20 plus pyTivo patches) | **mutagen ≥ 1.45** | The pyTivo patch added `MediaKind`/`HDVideo`; replaced by a local `MEDIA_KIND_TV_SHOW = 10` constant |
| `zeroconf.py` (python-zeroconf 0.19, unmodified) | **zeroconf ≥ 0.131** | API changes handled in `beacon.py` |
| `pytz` (imported but undeclared) | stdlib `datetime.timezone.utc` | |
| — | **legacy-cgi** (Python ≥ 3.13 only) | Cheetah3 still does `import cgi`, which 3.13 removed |

Optional extras: Pillow (photos), psutil (Settings drive list), and on
Windows pywin32 + comtypes (service, tray, VideoReDo), plus wxPython
(tray). They are declared in `requirements.txt` and `pyproject.toml`.

## 3. Changes by module

### Core
- **`config.py`**
  - Uses `ConfigParser(interpolation=None)`, so `%` in paths or ffmpeg
    params no longer breaks parsing.
  - Reads config files as UTF-8, falling back to cp1252 for files written
    by the Py2 version. Writes UTF-8.
  - `get_tsn()` / `getOptres()` use explicit `has_option` lookups (TiVo →
    SD/HD/4K → Server) instead of bare `try/except` chains.
  - Integer division for bitrates, and `reduce` imported.
  - `get_bin()` caches misses, so there's no warning per listed file.
    `logger.warn` → `logger.warning`.
  - New `get_server_bool()` helper.
  - `get_ip()` closes its socket and survives having no network.
- **`httpserver.py`**
  - Replaced the removed `cgi` module: `urllib.parse.parse_qs` for
    queries, and the `email` parser for multipart POSTs.
  - `headers.getheader` → `headers.get`. Responses are encoded to UTF-8
    bytes and gzipped with `gzip.compress`.
  - Client disconnects mid-transfer (normal for TiVos) are logged as one
    line rather than a traceback, in both `handle()` and `finish()`.
  - **Security:** HTML-escapes the query values echoed back by the
    "Unsupported Command" page (it was a reflected-XSS vector), and the
    Referer used in reload messages.
  - Works without a beacon (useful for tests and service mode).
- **`plugin.py`**
  - Shared `list_files()`, `cached_file_list()`, `SortList`, `shuffle()`
    and `move_to_front()` replace three copy-pasted directory walkers in
    the video, music and photo plugins.
  - `cmp`-based sorts became `key=` sorts with the same ordering.
  - `EncodeUnicode` now returns `str`, since Cheetah3 is text-based.
  - Plugins load via `importlib`, and errors are logged with a traceback.
  - On Python 3.13+, a narrow `sys.unraisablehook` filter drops one benign
    Cheetah3 C-extension message (see Hiccups).
- **`lrucache.py`**: rewritten on `OrderedDict`, with O(1) operations and
  thread safety. The old version relied on `__cmp__`, which Py3 ignores,
  and re-heapified on every read. Same public API, including `mtime()`.
- **`beacon.py`**
  - Ported to the modern zeroconf API: `ServiceInfo(addresses=…,
    server=…)`, `ServiceBrowser(listener=…)`, `parsed_addresses()`, and
    bytes TXT properties decoded.
  - Beacon packets are bytes. `thread` → `threading`.
  - The TCP beacon name lookup now has a 5 s timeout; it could hang
    indefinitely before.
  - The Desktop zeroconf record is now unregistered at shutdown.
- **`metadata.py`**
  - mutagen freeform atoms (bytes) are decoded, and `plistlib.loads` is
    used for iTunes plists.
  - Text/`.nfo` files are read as UTF-8 (with or without BOM) or cp1252.
  - MAK hashing operates on bytes. The deprecated
    `datetime.utcfromtimestamp/utcnow` calls are replaced with a helper
    that keeps the naive ISO format TiVos expect.
  - Malformed `.nfo` year and rating values no longer crash the parser.
- **`turing.py`**: `izip`/`xrange` replaced, bytes buffers, and `//` in
  the struct format strings.

### Plugins
- **video / transcode**
  - Removed the `subprocess.Popen._make_inheritable` monkeypatch, which
    crashed at import on Windows under Py3.
  - Removed the cp1252 re-encoding of file names: Py3 passes Unicode
    arguments to Windows directly.
  - ffmpeg output decoded with `errors='replace'`. Chunked-transfer
    framing and the faked TiVo header are built as bytes.
  - `kill()` now uses `Popen.terminate()/kill()` on every OS (it was
    ctypes `TerminateProcess` and a SIGTERM loop).
  - `VideoDetails` is a `collections.abc.MutableMapping`.
  - Container `UniqueId` CRCs are kept **signed**, as Py2 produced them,
    so the IDs a TiVo already knows don't change.
- **music**: bytes-safe chunked streaming and playlist parsing (BOM
  aware). ffmpeg duration probing uses a real timeout. Per-seed
  `random.Random` replaces reseeding the global RNG under a lock.
- **photo**
  - `Image.ANTIALIAS` (removed in Pillow 10) → `LANCZOS`.
  - EXIF regexes run on bytes; `StringIO` → `BytesIO`.
  - ffmpeg fallback uses temp files and timeouts.
  - A shared `run_ffmpeg()` helper replaces two copy-pasted wait loops.
- **settings**
  - Fixed the implicit relative import of `buildhelp`.
  - `print` statements → logging.
  - Disk usage via `shutil.disk_usage`; hidden-file check via
    `st_file_attributes`; volume labels via ctypes, dropping the pywin32
    requirement for this page. A missing psutil is reported, not fatal.
  - The share-type dropdown no longer lists `__pycache__`.
- **togo**
  - `urllib2`/`cookielib`/`urlparse` → `urllib.request`/`http.cookiejar`/
    `urllib.parse`, and `cgi.escape` → `html.escape`.
  - Worker threads use `threading`; `pytz` is dropped.
  - Decoder (tivolibre/tivodecode) failures now report the decoder's own
    error in the transfer status. Its stderr is drained in a background
    thread so it can't block. The MAK is masked in that message.
- **desktop / vrd / videoredo**: fixed relative imports and file I/O.
  comtypes is optional: without it the VRD plugin still loads and reports
  that VideoReDo is unavailable. VideoReDo version probing no longer sets
  `is_pro` before confirming the COM object.

### Entry points and packaging
- **`pyTivo.py`**: requires 3.9+. Removed the process-wide
  `ssl._create_default_https_context = ssl._create_unverified_context`
  hack, which disabled certificate checking for *every* HTTPS call. Closes
  the server socket on exit/restart.
- **`pyTivoService.py`**: `details[0]` → `details.winerror`; the log file
  is opened as line-buffered UTF-8.
- **`UserInterface/pyTivoTray.py`, `Icons.py`**
  - Ported to wxPython 4 (Phoenix): `wx.adv.TaskBarIcon`,
    `wx.adv.EVT_TASKBAR_LEFT_DCLICK`, `wx.Icon(bitmap)`, `Menu.Append`.
  - `UpdateIcon()` marshals to the GUI thread (it was called from worker
    threads). HTTP calls have timeouts and decode responses.
  - The broken Mac paths are fixed. No more `shell=True` string commands.
  - When running from source without the Electron app, "Open Desktop"
    opens the web UI.
- **`pyTivoConfigurator.pyw`**: Tkinter → `tkinter` module names; a
  cancelled folder picker no longer writes `.`.
- **PyInstaller specs**: removed options PyInstaller 6 dropped (`cipher`,
  `win_private_assemblies`, `win_no_prefer_redirects`). Hidden imports now
  use `collect_submodules('Cheetah')` and `collect_submodules('zeroconf')`.
- **Repo hygiene**:
  - Added `requirements.txt`, `pyproject.toml` (with ruff and pytest
    config) and a `tests/` suite.
  - `README` → `README.md`, rewritten (it still said Python 2.5).
  - Expanded `.gitignore`.
  - Untracked `pyTivo.conf` (`git rm --cached`; your local file is kept).
    It is written by the Settings page and can contain your MAK. Use
    `pyTivo.conf.dist` as the template.

## 4. Pre-existing bugs fixed along the way

These were present in the original Py2 code, not just the port:

1. `transcode.py`: the tivolibre branch built its command with the
   *tivodecode* path, so tivolibre was never used when transcoding `.tivo`
   files.
2. `togo.py` `get_out_file()`: the "old style" naming fallback sat inside
   `if url in basic_meta`, so a recording without cached metadata returned
   `None` and the download crashed. Its retry loop also mutated an aliased
   list, so names grew as `x (2) (3) (4)…`.
3. `togo.py` `UnqueueAll` never sent an HTTP response (the tray's "cancel
   all" request hung).
4. `togo.py` VideoReDo delete-on-success could spin forever in
   `while True: try: os.remove…`. It now retries for 30 s and logs.
5. `video.py`: `config.getTivoHeight >= 720` compared a *function* to an
   int (always True in Py2, TypeError in Py3).
6. `video.py`: a resumed transfer raised KeyError on
   `status[tivo_name][path]` the first time.
7. `video.py`: `'…%s…' % fname, tivo_name` was a format-argument bug;
   the "Done sending" log line reported `rate*1024*1024` as Mb/s, and
   passthrough transfers logged the per-second byte counter as the total.
8. `video.py`: `Content-Range` end was `size - offset - 1`. It is now
   `size - 1`, per RFC 7233 (see Hiccups).
9. `metadata.py`: the MP4 `tven` "SnEn" branch tested the key (`'tven'`)
   instead of the value.
10. `transcode.py`: `select_audiolang` could hit an unbound variable, or
    concatenate `None`, when no video stream mapping was found.
11. `photo.py`: `rotate()` without `expand=True` cropped rotated
    portrait photos to the original landscape canvas.
12. `music.py`: an `http://` playlist referenced the undefined
    `local_path`.

## 5. Verification

### Automated: `python -m pytest` (37 tests, ~2 s)
- **Unit:** config parsing and fallbacks, bitrate maths, cp1252 legacy
  configs, the LRU cache, the Turing cipher (round-trip, plus decrypting
  a synthetic encrypted `.tivo` header), and `.txt`/`.nfo`/TiVo-details/
  MP4-atom metadata.
- **End-to-end:** starts the real server on an ephemeral port with video,
  music and photo shares holding media generated by ffmpeg, then sends
  TiVo-style requests:
  - QueryServer, QueryFormats, root and share containers, TVBusQuery
  - Transcoding an MP4 into a TiVo-headered MPEG-PS stream
  - WAV → MP3 streaming and photo scaling
  - The Settings page, settings JSON and a POSTed settings update
  - The ToGo page and the path-traversal and XSS guards
- Passes on Python 3.11.9 and 3.13. `ruff` (pyflakes + syntax) is clean,
  and every file compiles under `-W error`.

### Live, against the TiVos on this network (read-only)
| Check | Result |
|---|---|
| Zeroconf discovery | Found **Family Room** (192.168.68.78, `tcd/Keystone`, TiVo Edge) and **Paella Tivo** (192.168.68.82, `tcd/Series5`, Roamio) |
| TCP beacon name exchange | Roamio answered with its name. The Edge doesn't listen on port 2190, so it falls back to its IP (handled) |
| TLS + MAK digest auth via the scoped SSL context | Works on both |
| `GetShowsList` | 666 and 584 recordings parsed |
| NPL page | Renders for both |
| Queued ToGo download (temp folder, `ts_format`) | Complete 32 MB recording at ~95 Mb/s, correct sortable file name |
| Stop mid-transfer (2.4 GB recording) | Transfer stops, partial file removed, queue empties |
| ToGo with decrypt enabled | Fails cleanly with "tivolibre.exe failed (exit code 1): Java Runtime Environment not found." |
| Simulated TiVo pull from your `FrameWork Laptop` share (Roamio TSN) | Listing, TVBusQuery and a streamed MKV (H.264 copied, E-AC3 5.1 → AC-3) with valid TS sync |
| Frozen `pyTivo.exe` (PyInstaller 6.22) | Built, served every endpoint, shut down cleanly on `Quit` |

Nothing was written to your TiVo or to `C:\Users\David\Videos\Tivo`. Test
downloads went to temp folders that were then deleted. The temporary copy
of your config, which contained the MAK, was deleted right after loading.

## 6. Potential hiccups and known issues

**Will affect you now**
1. **ToGo decryption needs Java.** Your config has `togo_decode = true`,
   but `bin/tivolibre.exe` is a Jar2Exe wrapper and there's no JRE on this
   machine. Downloads with "decrypt" checked fail (now with a clear
   message). Fix: install a Java runtime (or put a `jre` folder next to
   `tivolibre.exe`, per `bin/README`), or set `togo_decode = false` to keep
   `.tivo` files.
2. **The TiVo Edge isn't recognized as 4K.** `config.is4Ktivo()` only
   knows TSN prefixes `849`/`8F9` (Bolt). Your Edge's TSN starts with
   `D6E`, so pyTivo treats it as plain HD and won't pass HEVC through to
   it. This is upstream behavior and unchanged. Adding the Edge prefixes
   is a one-line change once confirmed.
3. **The Edge doesn't answer the port-2190 TCP beacon**, so code paths
   that ask a TiVo for its name over the beacon show its IP instead. ToGo
   uses the zeroconf name, so it's unaffected.

**Behavior changes to keep an eye on**
4. **Relaxed TLS, scoped to ToGo only.** TiVo connections skip certificate
   checks (TiVos are self-signed) and allow older TLS (`SECLEVEL=0`, TLS
   1.0 minimum, legacy renegotiation) so older models can connect under
   OpenSSL 3. Access is still protected by the MAK. Everything else in the
   process now verifies certificates normally.
5. **`Content-Range` on resumed transfers** is now standards-correct. The
   old value was wrong but TiVos coped with it. If resuming a paused
   transfer misbehaves, check this first (`plugins/video/video.py`).
6. Config files are now **written** as UTF-8. Non-ASCII share names in an
   old cp1252 file are read correctly and re-saved as UTF-8 the next time
   the Settings page saves.
7. Photo rotation now expands the canvas. A seeded shuffle gives a
   different (still stable) order than the Py2 version did.

**Upstream / ecosystem risks**
8. **Cheetah3 is barely maintained** (latest release 3.2.6.post1). On
   Python 3.13 it needs `legacy-cgi`, and its C NameMapper emits "Exception
   ignored in PyMapping_HasKeyString" noise. That is filtered narrowly in
   `plugin.py`, and output is unaffected. Python 3.14+ may need more
   workarounds; a longer-term move to Jinja2 would remove this dependency,
   but it means rewriting ten templates.
9. **The bundled `bin/ffmpeg.exe` is version 3.3.1 (2017).** It worked
   for everything tested, but a current ffmpeg will handle newer codecs
   better. ffmpeg's console output format is what `video_info()` parses,
   so re-run the tests after upgrading.
10. The tray's update check still calls `pytivodesktop.com` over plain
    HTTP. The site may no longer exist; failures are handled quietly.

**Not verified (needs hands-on testing)**
11. **The TiVo pulling video from the PC on the actual TiVo UI.** The
    server side was simulated with real TSNs and files, but a person needs
    to browse to the share on the TiVo. Steps:
    `python pyTivo.py` → on the TiVo, open *My Shows* → your PC
    ("FrameWork Laptop") → pick a video → *Transfer*.
12. Tray app and configurator GUIs (import-checked, not clicked through);
    the Windows service install/start; the VideoReDo post-processing (needs
    VideoReDo, which is discontinued); the pyTivo Desktop Electron app (not
    in this repo); macOS and Linux.
13. The service and tray PyInstaller specs were updated but not built.
    Build from a clean virtualenv: the global environment pulls unrelated
    packages (pygame, charset_normalizer) into the exe.

**Housekeeping**
14. After an aborted stream, ffmpeg is kept alive for 10 minutes so the
    TiVo can resume (upstream design; it is then reaped).
15. A transfer the user stops shows `state=finished` with no error in the
    ToGo status (upstream behavior).
16. All of this work is uncommitted on branch `py3-modernization`.
