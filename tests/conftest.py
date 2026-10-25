import os
import shutil
import subprocess
import sys
import textwrap

import pytest

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
if ROOT not in sys.path:
    sys.path.insert(0, ROOT)


def find_ffmpeg():
    exe = 'ffmpeg.exe' if sys.platform == 'win32' else 'ffmpeg'
    bundled = os.path.join(ROOT, 'bin', exe)
    if os.path.isfile(bundled):
        return bundled
    return shutil.which('ffmpeg')


FFMPEG = find_ffmpeg()
needs_ffmpeg = pytest.mark.skipif(not FFMPEG, reason='ffmpeg not available')


def make_media(path, *args):
    """Generate a small media file with ffmpeg's built-in test sources."""
    subprocess.run([FFMPEG, '-v', 'error', '-y'] + list(args) + [path],
                   check=True, stdin=subprocess.DEVNULL)
    return path


def write_config(path, text):
    with open(path, 'w', encoding='utf-8') as f:
        f.write(textwrap.dedent(text))
    return path


@pytest.fixture
def load_config(tmp_path):
    """Load a pyTivo.conf from text and return the config module."""
    import config

    def load(text):
        conf = write_config(str(tmp_path / 'pyTivo.conf'), text)
        config.init(['-c', conf])
        return config
    return load
