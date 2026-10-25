"""Download the current ffmpeg release into this folder (Windows).

pyTivo looks for ffmpeg in its bin/ folder first and then on the PATH.
A current static ffmpeg.exe is larger than GitHub's 100 MB file limit,
so it is no longer kept in the repository; run this script instead:

    python bin/get_ffmpeg.py

It fetches the "release essentials" build from gyan.dev (one of the
Windows builds linked from ffmpeg.org), checks its published SHA-256,
and extracts only ffmpeg.exe. On macOS or Linux, install ffmpeg with
your package manager (e.g. ``brew install ffmpeg`` or
``sudo apt install ffmpeg``) instead.
"""
import hashlib
import os
import sys
import tempfile
import urllib.request
import zipfile

BUILD_URL = 'https://www.gyan.dev/ffmpeg/builds/ffmpeg-release-essentials.zip'
BIN_DIR = os.path.dirname(os.path.abspath(__file__))


def download(url, dest):
    with urllib.request.urlopen(url, timeout=60) as response, \
         open(dest, 'wb') as out:
        total = int(response.headers.get('Content-Length', 0))
        done = 0
        while True:
            block = response.read(1024 * 1024)
            if not block:
                break
            out.write(block)
            done += len(block)
            if total:
                print('\r  %3d%% of %d MB' % (done * 100 // total,
                                              total // 2**20), end='')
    print()


def sha256(path):
    digest = hashlib.sha256()
    with open(path, 'rb') as f:
        for block in iter(lambda: f.read(1024 * 1024), b''):
            digest.update(block)
    return digest.hexdigest()


def main():
    if sys.platform != 'win32':
        sys.exit(__doc__.split('\n\n')[-1].strip())

    with urllib.request.urlopen(BUILD_URL + '.sha256', timeout=60) as r:
        expected = r.read().decode('ascii').split()[0].lower()

    with tempfile.TemporaryDirectory() as tmp:
        archive = os.path.join(tmp, 'ffmpeg.zip')
        print('Downloading', BUILD_URL)
        download(BUILD_URL, archive)

        actual = sha256(archive)
        if actual != expected:
            sys.exit('Checksum mismatch: expected %s, got %s'
                     % (expected, actual))

        with zipfile.ZipFile(archive) as z:
            members = [n for n in z.namelist()
                       if n.endswith('/bin/ffmpeg.exe')]
            if len(members) != 1:
                sys.exit('ffmpeg.exe not found in the archive')
            target = os.path.join(BIN_DIR, 'ffmpeg.exe')
            partial = target + '.partial'
            with z.open(members[0]) as src, open(partial, 'wb') as dst:
                while True:
                    block = src.read(1024 * 1024)
                    if not block:
                        break
                    dst.write(block)
            os.replace(partial, target)
            print('Installed', members[0].split('/')[0], 'to', target)


if __name__ == '__main__':
    main()
