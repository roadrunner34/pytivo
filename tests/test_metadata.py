import io


from conftest import needs_ffmpeg, make_media

import metadata


def test_text_metadata_utf8_bom_and_cp1252(tmp_path):
    video = tmp_path / 'Show.mpg'
    video.write_bytes(b'')
    (tmp_path / 'Show.mpg.txt').write_bytes(
        b'\xef\xbb\xbftitle : Caf\xc3\xa9\nvActor : One\nvActor : Two\n'
        b'tvRating : TV-PG\n# comment\n')
    (tmp_path / 'default.txt').write_bytes(b'callsign : Caf\xe9TV\n')

    data = metadata.from_text(str(video))
    assert data['title'] == 'Caf\xe9'
    assert data['vActor'] == ['One', 'Two']
    assert data['tvRating'] == 4
    assert data['callsign'] == 'Caf\xe9TV'


def test_episode_nfo(tmp_path):
    show = tmp_path / 'Show'
    show.mkdir()
    (show / 'tvshow.nfo').write_text(
        '<tvshow><title>Series</title><genre>Drama</genre></tvshow>',
        encoding='utf-8')
    episode = show / 'S01E02.mkv'
    episode.write_bytes(b'')
    (show / 'S01E02.nfo').write_text(
        '<episodedetails><title>Pilot</title><season>1</season>'
        '<episode>2</episode><aired>2001-02-03</aired><rating>8.0</rating>'
        '<actor><name>Someone</name></actor></episodedetails>\n'
        'http://example.com/scraper-url\n', encoding='utf-8')

    data = metadata.from_nfo(str(episode))
    assert data['episodeTitle'] == 'Pilot'
    assert data['episodeNumber'] == '102'
    assert data['originalAirDate'] == '2001-02-03T00:00:00Z'
    assert data['starRating'] == 6
    assert data['vActor'] == ['Someone']
    assert data['vProgramGenre'] == ['Drama']


def test_from_details_xml():
    xml = (b'<TvBusMarshalledStruct><showing><program>'
           b'<title>Movie</title><description>Plot. Copyright Tribune Media '
           b'Services, Inc.</description><vActor><element>A</element>'
           b'<element>B</element></vActor>'
           b'<mpaaRating value="3">PG_13</mpaaRating></program>'
           b'<tvRating value="4"/></showing></TvBusMarshalledStruct>')
    data = metadata.from_details(xml)
    assert data['title'] == 'Movie'
    assert data['description'] == 'Plot.'
    assert data['vActor'] == ['A', 'B']
    assert data['mpaaRating'] == 3
    assert data['tvRating'] == 4


def test_dump_writes_text():
    out = io.StringIO()
    metadata.dump(out, {'title': 'Caf\xe9', 'vActor': ['A'], 'tvRating': 4})
    assert out.getvalue().splitlines() == ['title: Caf\xe9', 'vActor: A',
                                           'tvRating: PG']


def test_basic_has_naive_utc_air_date(tmp_path):
    video = tmp_path / 'Title Here.avi'
    video.write_bytes(b'')
    data = metadata.basic(str(video), mtime=86400)
    assert data['title'] == 'Title Here'
    assert data['originalAirDate'] == '1970-01-02T00:00:00'


@needs_ffmpeg
def test_mp4_atoms(tmp_path):
    from mutagen.mp4 import MP4, MP4FreeForm

    path = make_media(str(tmp_path / 'ep.mp4'), '-f', 'lavfi', '-i',
                      'testsrc=size=64x48:rate=5:duration=1', '-c:v',
                      'mpeg4')
    mp4 = MP4(path)
    mp4['stik'] = [10]
    mp4['tvsh'] = ['Series']
    mp4['\xa9nam'] = ['Episode']
    mp4['tvsn'] = [3]
    mp4['tves'] = [4]
    mp4['----:com.apple.iTunes:iTunEXTC'] = [MP4FreeForm(b'us-tv|TV-14|500|')]
    mp4.save()

    data = metadata.from_moov(path)
    assert data['seriesTitle'] == 'Series'
    assert data['episodeTitle'] == 'Episode'
    assert data['episodeNumber'] == '304'
    assert data['tvRating'] == 5
