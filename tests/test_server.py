"""End-to-end tests: run the real HTTP server and talk to it like a TiVo."""
import http.client
import threading
import xml.dom.minidom
from urllib.parse import quote, urlencode

import pytest

from conftest import FFMPEG, make_media, needs_ffmpeg, write_config

HD_TSN = '7460001234567890'
SD_TSN = '5400001234567890'


@pytest.fixture(scope='module')
def server(tmp_path_factory):
    base = tmp_path_factory.mktemp('pytivo')
    videos = base / 'Vidéos'
    music = base / 'Music'
    photos = base / 'Photos'
    togo = base / 'ToGo'
    for d in (videos, music, photos, togo):
        d.mkdir()
    (videos / 'Sub Folder').mkdir()

    if FFMPEG:
        make_media(str(videos / 'Clip Été.mp4'), '-f', 'lavfi', '-i',
                   'testsrc=size=320x240:rate=25:duration=1', '-f', 'lavfi',
                   '-i', 'sine=frequency=440:duration=1', '-c:v', 'mpeg4',
                   '-c:a', 'aac', '-strict', '-2', '-shortest')
        make_media(str(music / 'Tone.wav'), '-f', 'lavfi', '-i',
                   'sine=frequency=440:duration=1')
        (videos / 'Clip Été.mp4.txt').write_text(
            'title : Clip Title\nepisodeTitle : Épisode\n', encoding='utf-8')

    from PIL import Image
    Image.new('RGB', (640, 480), (200, 30, 30)).save(str(photos / 'Red.png'))

    conf = write_config(str(base / 'pyTivo.conf'), """
        [Server]
        port = 0
        ffmpeg = %s
        zeroconf = false
        beacon = listen-only
        tivo_mak = 0123456789
        togo_path = %s

        [Videos]
        type = video
        path = %s

        [Music]
        type = music
        path = %s

        [Photos]
        type = photo
        path = %s
    """ % (FFMPEG or '', togo, videos, music, photos))

    import config
    import httpserver
    config.init(['-c', conf])
    config.init_logging()

    httpd = httpserver.TivoHTTPServer(('127.0.0.1', 0),
                                      httpserver.TivoHTTPHandler)
    for section, settings in config.getShares():
        httpd.add_container(section, settings)
    thread = threading.Thread(target=httpd.serve_forever, daemon=True)
    thread.start()

    yield {'port': httpd.server_address[1], 'conf': conf, 'videos': videos}

    httpd.shutdown()
    httpd.server_close()


def request(server, path, method='GET', body=None, headers=None):
    conn = http.client.HTTPConnection('127.0.0.1', server['port'], timeout=60)
    hdrs = {'tsn': HD_TSN}
    hdrs.update(headers or {})
    conn.request(method, path, body=body, headers=hdrs)
    response = conn.getresponse()
    data = response.read()
    conn.close()
    return response, data


def command(server, **params):
    return request(server, '/TiVoConnect?' + urlencode(params))


def parse(data):
    return xml.dom.minidom.parseString(data)


def texts(doc, tag):
    return [n.firstChild.data for n in doc.getElementsByTagName(tag)
            if n.firstChild]


def test_query_server(server):
    response, data = command(server, Command='QueryServer')
    assert response.status == 200
    assert texts(parse(data), 'InternalName') == ['pyTivo']


def test_query_formats(server):
    _, data = command(server, Command='QueryFormats', SourceFormat='video/x-tivo-mpeg')
    assert 'video/x-tivo-mpeg-ts' in texts(parse(data), 'ContentType')


def test_root_container(server):
    response, data = command(server, Command='QueryContainer', Container='/')
    assert response.status == 200
    doc = parse(data)
    titles = texts(doc, 'Title')
    for share in ('Videos', 'Music', 'Photos'):
        assert share in titles
    assert 'Settings' not in titles      # not a TiVo media container


def test_unsupported_command_is_escaped(server):
    response, data = command(server, Command='<script>')
    assert response.status == 404
    assert b'<script>' not in data and b'&lt;script&gt;' in data


def test_static_content_and_info_page(server):
    response, data = request(server, '/main.css')
    assert response.status == 200 and response.getheader('Content-Type') == 'text/css'
    response, data = request(server, '/')
    assert response.status == 200 and b'Settings' in data


def test_path_traversal_blocked(server):
    response, _ = request(server, '/Videos/../../pyTivo.conf')
    assert response.status == 404


@needs_ffmpeg
def test_video_container(server):
    response, data = command(server, Command='QueryContainer',
                             Container='Videos', ItemCount='8')
    assert response.status == 200
    doc = parse(data)
    titles = texts(doc, 'Title')
    assert 'Sub Folder' in titles
    assert 'Clip Title' in titles          # from the .txt metadata file
    assert texts(doc, 'TotalItems')[0] == '2'
    for uid in texts(doc, 'UniqueId'):
        int(uid)
    urls = texts(doc, 'Url')
    assert '/Videos/Clip%20%C3%89t%C3%A9.mp4' in urls


@needs_ffmpeg
def test_video_details(server):
    _, data = command(server, Command='TVBusQuery', Container='Videos',
                      File='/Clip Été.mp4')
    doc = parse(data)
    assert texts(doc, 'title') == ['Clip Title']
    assert texts(doc, 'episodeTitle') == ['Épisode']


@needs_ffmpeg
def test_video_transfer_transcodes_with_tivo_header(server):
    response, data = request(server, '/Videos/' + quote('Clip Été.mp4') +
                             '?Format=video/x-tivo-mpeg')
    assert response.status == 206
    assert response.getheader('Transfer-Encoding') == 'chunked'
    # A faked TiVo header, followed by the MPEG-2 program stream
    assert data[:4] == b'TiVo'
    header_size = int.from_bytes(data[10:14], 'big')
    assert b'Clip Title' in data[:header_size]
    assert data[header_size:header_size + 4] == b'\x00\x00\x01\xba'

    _, status = command(server, Command='GetTransferStatus')
    assert b'Clip' in status


@needs_ffmpeg
def test_music_container_and_transcode(server):
    _, data = command(server, Command='QueryContainer', Container='Music')
    doc = parse(data)
    assert 'Tone' in texts(doc, 'Title')
    assert int(texts(doc, 'Duration')[0]) > 900

    response, data = request(server, '/Music/Tone.wav')
    assert response.status == 206
    assert data[:3] == b'ID3' or data[0] == 0xff     # an MP3 stream


def test_photo_container_and_scaling(server):
    _, data = command(server, Command='QueryContainer', Container='Photos')
    assert 'Red' in texts(parse(data), 'Title')   # extension is hidden

    response, data = request(server, '/Photos/Red.png?Width=320&Height=240')
    assert response.status == 200
    assert response.getheader('Content-Type') == 'image/jpeg'
    assert data[:2] == b'\xff\xd8'

    from PIL import Image
    import io
    assert Image.open(io.BytesIO(data)).size == (320, 240)


def test_settings_page_and_json(server):
    response, data = command(server, Command='Settings', Container='Settings')
    assert response.status == 200 and b'Global Server Settings' in data

    import json
    _, data = command(server, Command='GetSettings', Container='Settings')
    settings = json.loads(data)
    assert settings['Shares']['Videos']['type'] == 'video'
    assert settings['Server']['zeroconf'] == 'false'


def test_update_settings_post(server):
    body = urlencode({
        'Command': 'UpdateSettings', 'Container': 'Settings',
        'Server.port': '0', 'Server.ffmpeg': FFMPEG or ' ',
        'Server.zeroconf': 'false', 'Server.tivo_mak': '0123456789',
        'Server.togo_path': 'ToGo', 'Server.beacon': 'listen-only',
        'Server.new__setting': 'debug', 'Server.new__value': 'false',
        'Section_Map': '',
        'new_Section': ' ',
    })
    response, data = request(server, '/TiVoConnect', 'POST', body,
                             {'Content-Type':
                              'application/x-www-form-urlencoded'})
    assert response.status == 200 and b'Settings Saved' in data
    with open(server['conf'], encoding='utf-8') as f:
        saved = f.read()
    assert 'debug = false' in saved
    assert 'togo_path = ToGo' in saved


def test_togo_page_without_tivo(server):
    response, data = command(server, Command='NPL', Container='ToGo')
    assert response.status == 200
    assert b'<html' in data.lower()


def test_transfer_counts(server):
    import json
    _, data = command(server, Command='GetActiveTransferCount')
    assert json.loads(data)['count'] == 0
    _, data = command(server, Command='GetTotalQueueCount', Container='ToGo')
    assert json.loads(data)['count'] == 0
