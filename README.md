# pyTivo

pyTivo lets you stream most videos from your PC to an unhacked TiVo, and
copy recordings from the TiVo back to your PC ("ToGo"). It uses the TiVo
HMO server protocol. It will guess whether your video is 4:3 or 16:9 and
pad it if needed, and it won't transcode video that your TiVo already
supports.

This fork runs on **Python 3.9+** (tested on 3.11 and 3.13). See
[MODERNIZATION.md](MODERNIZATION.md) for what changed from the Python 2
version.

## Requirements

- Python 3.9 or newer
- [FFmpeg](https://ffmpeg.org/) for transcoding. On Windows, fetch the
  current release into `bin/` with `python bin/get_ffmpeg.py` (it's too
  large to keep in git). Elsewhere, install it with your package manager;
  pyTivo also finds ffmpeg on the `PATH`.
- Python packages:

  ```sh
  python -m pip install -r requirements.txt
  ```

  The core requirements are Jinja2, mutagen and zeroconf. Pillow (photo
  scaling), psutil (Settings page drive list) and, on Windows, pywin32 and
  comtypes (service, tray app and VideoReDo) are optional.

- Optional: `tivolibre.exe` / `tivodecode` to decrypt `.tivo` files.
  The bundled `bin/tivolibre.exe` is a wrapped Java program and needs a
  Java runtime (see `bin/README`).

## Usage

Copy `pyTivo.conf.dist` to `pyTivo.conf` and edit three settings:

1. `ffmpeg=` — the full path to ffmpeg, including the file name
2. `[<name of share>]` — a section per share
3. `path=` — the absolute path to your media

Then run:

```sh
python pyTivo.py
```

Browse to <http://localhost:9032/> for the web Settings page.

## Windows service

```sh
python pyTivoService.py --startup auto install
python pyTivoService.py remove
```

## Tests

```sh
python -m pip install pytest
python -m pytest
```

The end-to-end tests run a real pyTivo server; the media tests use ffmpeg
(the bundled one or one on `PATH`) and are skipped without it.

## Credits

pyTivo was created by Jason Michalski ("armooo"). Contributors include
Kevin R. Keegan, William McBrine, and Terry Mound ("wgw").
