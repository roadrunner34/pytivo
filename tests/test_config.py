import pytest


def test_strtod_suffixes():
    import config
    assert config.strtod('2K') == 2000
    assert config.strtod('2Ki') == 2048
    assert config.strtod('2MB') == 16000000
    assert config.strtod('448k') == 448000
    with pytest.raises(SyntaxError):
        config.strtod('fast')


def test_bitrates_are_integers(load_config):
    # Py3 true division used to turn these into '448.0k' style strings
    config = load_config("""
        [Server]
        audio_br = 320K
        video_br = 12Mi
    """)
    assert config.getAudioBR() == '320k'
    assert config.getMaxAudioBR() == 448
    assert config.getVideoBR() == '12582k'
    assert config.getMaxVideoBR() == '30000k'
    assert config.getBuffSize('746000000000000') == '4096k'


def test_get_tsn_fallback_order(load_config):
    config = load_config("""
        [Server]
        audio_lang = eng
        ffmpeg_pram = -threads 2 %s

        [_tivo_HD]
        audio_lang = spa

        [_tivo_7460001]
        audio_lang = fre
    """)
    assert config.get_tsn('audio_lang', '7460001') == 'fre'   # TiVo section
    assert config.get_tsn('audio_lang', '7460002') == 'spa'   # HD section
    assert config.get_tsn('audio_lang', '5400001') == 'eng'   # Server
    assert config.get_tsn('missing', '7460001') is None
    # '%' must survive: values are never interpolated
    assert config.getFFmpegPrams('7460001') == '-threads 2 %s'


def test_tivo_classes():
    import config
    assert config.isHDtivo('7460001')
    assert not config.isHDtivo('5400001')
    assert not config.isHDtivo('6490001')       # 649 is SD letterbox
    assert config.is4Ktivo('8490001')
    assert not config.is4Ktivo('')
    assert config.is_ts_capable('7460001')
    assert config.nearestTivoHeight(700) == 720
    assert config.nearestTivoWidth(600) == 544


def test_shares_sorted_with_builtins(load_config):
    config = load_config("""
        [Server]
        port = 9032

        [Zebra]
        type = video
        path = /z

        [Apple]
        type = music
        path = /a
    """)
    names = [name for name, _ in config.getShares()]
    assert names[:3] == ['Apple', 'Zebra', 'Settings']


def test_nosettings_hides_settings(load_config):
    config = load_config("""
        [Server]
        nosettings = true
    """)
    assert 'Settings' not in [name for name, _ in config.getShares()]


def test_legacy_cp1252_config(tmp_path):
    import config
    conf = tmp_path / 'pyTivo.conf'
    conf.write_bytes(b'[Caf\xe9]\ntype = video\npath = C:\\Vid\xe9os\n')
    config.init(['-c', str(conf)])
    assert config.config.get('Caf\xe9', 'path') == 'C:\\Vid\xe9os'
