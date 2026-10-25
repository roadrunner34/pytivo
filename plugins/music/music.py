import logging
import os
import re
import shutil
import subprocess
import sys
import tempfile
import urllib.request
from urllib.parse import unquote_plus
from xml.sax.saxutils import escape

import mutagen
from mutagen.easyid3 import EasyID3
from mutagen.mp3 import MP3
from lrucache import LRUCache
import config
from plugin import (Plugin, quote, unquote, list_files, move_to_front,
                    shuffle)
from templating import render
from plugins.video.transcode import kill

logger = logging.getLogger('pyTivo.music')

# determine if application is a script file or frozen exe
SCRIPTDIR = os.path.dirname(__file__)
if getattr(sys, 'frozen', False):
    SCRIPTDIR = os.path.join(sys._MEIPASS, 'plugins', 'music')

CLASS_NAME = 'Music'

PLAYLISTS = ('.m3u', '.m3u8', '.ram', '.pls', '.b4s', '.wpl', '.asx',
             '.wax', '.wvx')

TRANSCODE = ('.mp4', '.m4a', '.flc', '.ogg', '.wma', '.aac', '.wav',
             '.aif', '.aiff', '.au', '.flac')

TAGNAMES = {'artist': ['\xa9ART', 'Author'],
            'title': ['\xa9nam', 'Title'],
            'album': ['\xa9alb', 'WM/AlbumTitle'],
            'date': ['\xa9day', 'WM/Year'],
            'genre': ['\xa9gen', 'WM/Genre']}

BLOCKSIZE = 64 * 1024

# Search strings for different playlist types
asxfile = re.compile('ref +href *= *"([^"]*)"', re.IGNORECASE).search
wplfile = re.compile('media +src *= *"([^"]*)"', re.IGNORECASE).search
b4sfile = re.compile('Playstring="file:([^"]*)"').search
plsfile = re.compile(r'[Ff]ile(\d+)=(.+)').match
plstitle = re.compile(r'[Tt]itle(\d+)=(.+)').match
plslength = re.compile(r'[Ll]ength(\d+)=(\d+)').match

# Duration -- parse from ffmpeg output
durre = re.compile(r'.*Duration: ([0-9]+):([0-9]+):([0-9]+)\.([0-9]+),').search

def tmpl(name):
    return os.path.join(SCRIPTDIR, 'templates', name)

FOLDER_TEMPLATE = tmpl('container.tmpl')
PLAYLIST_TEMPLATE = tmpl('m3u.tmpl')
ITEM_TEMPLATE = tmpl('item.tmpl')

class FileData:
    def __init__(self, name, isdir):
        self.name = name
        self.isdir = isdir
        self.isplay = os.path.splitext(name)[1].lower() in PLAYLISTS
        self.title = ''
        self.duration = 0

def ffmpeg_duration(ffmpeg_path, fname, timeout=10):
    """Return the duration of a media file in milliseconds, using ffmpeg."""
    cmd = [ffmpeg_path, '-i', fname]
    with tempfile.TemporaryFile() as err_tmp:
        ffmpeg = subprocess.Popen(cmd, stderr=err_tmp,
                                  stdout=subprocess.DEVNULL,
                                  stdin=subprocess.DEVNULL)
        try:
            ffmpeg.wait(timeout=timeout)
        except subprocess.TimeoutExpired:
            kill(ffmpeg)
            return None
        err_tmp.seek(0)
        output = err_tmp.read().decode('utf-8', 'replace')

    d = durre(output)
    if not d:
        return 0
    return ((int(d.group(1)) * 3600 +
             int(d.group(2)) * 60 +
             int(d.group(3))) * 1000 +
            int(d.group(4)) * (10 ** (3 - len(d.group(4)))))

class Music(Plugin):

    CONTENT_TYPE = 'x-container/tivo-music'

    AUDIO = 'audio'
    DIRECTORY = 'dir'
    PLAYLIST = 'play'

    media_data_cache = LRUCache(300)
    recurse_cache = LRUCache(5)
    dir_cache = LRUCache(10)

    def send_file(self, handler, path, query):
        seek = int(query.get('Seek', [0])[0])
        duration = int(query.get('Duration', [0])[0])
        always = (handler.container.getboolean('force_ffmpeg') and
                  config.get_bin('ffmpeg'))
        fname = path

        ext = os.path.splitext(fname)[1].lower()
        needs_transcode = ext in TRANSCODE or seek or duration or always

        if not needs_transcode:
            try:
                fsize = os.path.getsize(fname)
            except OSError:
                handler.send_error(404)
                return
            handler.send_response(200)
            handler.send_header('Content-Length', fsize)
        else:
            handler.send_response(206)
            handler.send_header('Transfer-Encoding', 'chunked')
        handler.send_header('Content-Type', 'audio/mpeg')
        handler.end_headers()

        if needs_transcode:
            cmd = [config.get_bin('ffmpeg'), '-i', fname, '-vn']
            if ext in ['.mp3', '.mp2']:
                cmd += ['-acodec', 'copy']
            else:
                cmd += ['-ab', '320k', '-ar', '44100']
            cmd += ['-f', 'mp3', '-']
            if seek:
                cmd[-1:] = ['-ss', '%.3f' % (seek / 1000.0), '-']
            if duration:
                cmd[-1:] = ['-t', '%.3f' % (duration / 1000.0), '-']

            ffmpeg = subprocess.Popen(cmd, bufsize=BLOCKSIZE,
                                      stdout=subprocess.PIPE,
                                      stdin=subprocess.DEVNULL)
            while True:
                try:
                    block = ffmpeg.stdout.read(BLOCKSIZE)
                    handler.wfile.write(b'%x\r\n' % len(block))
                    handler.wfile.write(block)
                    handler.wfile.write(b'\r\n')
                except OSError as msg:
                    handler.server.logger.info(msg)
                    kill(ffmpeg)
                    break

                if not block:
                    break
            ffmpeg.stdout.close()
            ffmpeg.wait()
        else:
            with open(fname, 'rb') as f:
                try:
                    shutil.copyfileobj(f, handler.wfile)
                except OSError:
                    pass

        try:
            handler.wfile.flush()
        except OSError as msg:
            handler.server.logger.info(msg)

    def QueryContainer(self, handler, query):

        def AudioFileFilter(f, filter_type=None):
            ext = os.path.splitext(f)[1].lower()

            if ext in ('.mp3', '.mp2') or (ext in TRANSCODE and
                                           config.get_bin('ffmpeg')):
                return self.AUDIO
            else:
                file_type = False

                if not filter_type or filter_type.split('/')[0] != self.AUDIO:
                    if ext in PLAYLISTS:
                        file_type = self.PLAYLIST
                    elif os.path.isdir(f):
                        file_type = self.DIRECTORY

                return file_type

        def media_data(f):
            if f.name in self.media_data_cache:
                return self.media_data_cache[f.name]

            item = {}
            item['path'] = f.name
            item['part_path'] = f.name.replace(local_base_path, '', 1)
            item['name'] = os.path.basename(f.name)
            item['is_dir'] = f.isdir
            item['is_playlist'] = f.isplay
            item['params'] = 'No'

            if f.title:
                item['Title'] = f.title

            if f.duration > 0:
                item['Duration'] = f.duration

            if f.isdir or f.isplay or '://' in f.name:
                self.media_data_cache[f.name] = item
                return item

            ext = os.path.splitext(f.name)[1].lower()
            fname = f.name

            try:
                # If the file is an mp3, let's load the EasyID3 interface
                if ext == '.mp3':
                    audioFile = MP3(fname, ID3=EasyID3)
                else:
                    # Otherwise, let mutagen figure it out
                    audioFile = mutagen.File(fname)

                if audioFile:
                    # Pull the length from the FileType, if present
                    if audioFile.info.length > 0:
                        item['Duration'] = int(audioFile.info.length * 1000)

                    # Grab our other tags, if present
                    def get_tag(tagname, d):
                        if d.tags is None:
                            return ''
                        for tag in ([tagname] + TAGNAMES[tagname]):
                            try:
                                if tag in d:
                                    value = d[tag][0]
                                    if not isinstance(value, str):
                                        value = str(value)
                                    return value
                            except (KeyError, IndexError, ValueError):
                                pass
                        return ''

                    artist = get_tag('artist', audioFile)
                    title = get_tag('title', audioFile)
                    if artist == 'Various Artists' and '/' in title:
                        artist, title = [x.strip()
                                         for x in title.split('/', 1)]
                    item['ArtistName'] = artist
                    item['SongTitle'] = title
                    item['AlbumTitle'] = get_tag('album', audioFile)
                    item['AlbumYear'] = get_tag('date', audioFile)[:4]
                    item['MusicGenre'] = get_tag('genre', audioFile)
            except Exception as msg:
                logger.info('Unable to read tags from %s: %s', fname, msg)

            ffmpeg_path = config.get_bin('ffmpeg')
            if 'Duration' not in item and ffmpeg_path:
                millisecs = ffmpeg_duration(ffmpeg_path, fname)
                if millisecs is not None:
                    item['Duration'] = millisecs

            if 'Duration' in item and ffmpeg_path:
                item['params'] = 'Yes'

            self.media_data_cache[f.name] = item
            return item

        subcname = query['Container'][0]
        local_base_path = self.get_local_base_path(handler, query)

        if ('http://' not in subcname and
            not self.get_local_path(handler, query, must_exist=True)):
            handler.send_error(404)
            return

        if os.path.splitext(subcname)[1].lower() in PLAYLISTS:
            template = PLAYLIST_TEMPLATE
            files, total, start = self.get_playlist(handler, query)
        else:
            template = FOLDER_TEMPLATE
            files, total, start = self.get_files(handler, query,
                                                 AudioFileFilter)

        handler.send_xml(render(
            template, files=[media_data(f) for f in files], total=total,
            start=start, container=handler.cname, name=subcname,
            quote=quote, escape=escape))

    def QueryItem(self, handler, query):
        splitpath = [x for x in unquote_plus(query['Url'][0]).split('/') if x]
        path = os.path.join(handler.container['path'], *splitpath[1:])

        if path in self.media_data_cache:
            handler.send_xml(render(ITEM_TEMPLATE,
                                    file=self.media_data_cache[path],
                                    escape=escape))
        else:
            handler.send_error(404)

    def parse_playlist(self, list_name, recurse):

        ext = os.path.splitext(list_name)[1].lower()
        local_path = ''

        if ext in ('.m3u', '.pls'):
            charset = 'cp1252'
        else:
            charset = 'utf-8'

        url = list_name.find('http://')
        if url >= 0:
            list_name = list_name[url:]
            with urllib.request.urlopen(list_name, timeout=30) as list_file:
                data = list_file.read()
        else:
            local_path = os.path.dirname(list_name)
            with open(list_name, 'rb') as list_file:
                data = list_file.read()

        if data.startswith(b'\xef\xbb\xbf'):
            data = data[3:]
            charset = 'utf-8'
        lines = data.decode(charset, 'replace').splitlines()

        if ext in ('.wpl', '.asx', '.wax', '.wvx', '.b4s'):
            playlist = []
            for line in lines:
                if ext == '.wpl':
                    s = wplfile(line)
                elif ext == '.b4s':
                    s = b4sfile(line)
                else:
                    s = asxfile(line)
                if s:
                    playlist.append(FileData(s.group(1), False))

        elif ext == '.pls':
            names, titles, lengths = {}, {}, {}
            for line in lines:
                s = plsfile(line)
                if s:
                    names[s.group(1)] = s.group(2)
                else:
                    s = plstitle(line)
                    if s:
                        titles[s.group(1)] = s.group(2)
                    else:
                        s = plslength(line)
                        if s:
                            lengths[s.group(1)] = int(s.group(2))
            playlist = []
            for key in names:
                f = FileData(names[key], False)
                if key in titles:
                    f.title = titles[key]
                if key in lengths:
                    f.duration = lengths[key]
                playlist.append(f)

        else: # ext == '.m3u' or '.m3u8' or '.ram'
            duration, title = 0, ''
            playlist = []
            for line in lines:
                line = line.strip()
                if line:
                    if line.startswith('#EXTINF:'):
                        try:
                            duration, title = line[8:].split(',', 1)
                            duration = int(duration)
                        except ValueError:
                            duration = 0

                    elif not line.startswith('#'):
                        f = FileData(line, False)
                        f.title = title.strip()
                        f.duration = duration
                        playlist.append(f)
                        duration, title = 0, ''

        # Expand relative paths
        for item in playlist:
            if '://' not in item.name:
                name = item.name
                if not os.path.isabs(name):
                    name = os.path.join(local_path, name)
                item.name = os.path.normpath(name)

        if recurse:
            newlist = []
            for i in playlist:
                if i.isplay:
                    newlist.extend(self.parse_playlist(i.name, recurse))
                else:
                    newlist.append(i)

            playlist = newlist

        return playlist

    def get_files(self, handler, query, filterFunction=None):

        path = self.get_local_path(handler, query)

        file_type = query.get('Filter', [''])[0]

        recurse = query.get('Recurse', ['No'])[0] == 'Yes'

        def build(path, recurse):
            return list_files(path, lambda f: filterFunction(f, file_type),
                              recurse, FileData, keep_dirs=True)

        filelist = self.cached_file_list(path, recurse, build)

        # Sort it
        seed = ''
        start = ''
        sortby = query.get('SortOrder', ['Normal'])[0]
        if 'Random' in sortby:
            if 'RandomSeed' in query:
                seed = query['RandomSeed'][0]
                sortby += seed
            if 'RandomStart' in query:
                start = query['RandomStart'][0]
                sortby += start

        with filelist.lock:
            if filelist.unsorted or filelist.sortby != sortby:
                if 'Random' in sortby:
                    shuffle(filelist.files, seed)
                    if start:
                        local_base_path = self.get_local_base_path(handler,
                                                                   query)
                        start = unquote(start)
                        start = start.replace(os.path.sep + handler.cname,
                                              local_base_path, 1)
                        move_to_front(filelist.files, start,
                                      handler.server.logger)
                else:
                    # Directories first, then playlists, then by name
                    filelist.files.sort(key=lambda x: (not x.isdir,
                                                       not x.isplay, x.name))

                filelist.sortby = sortby
                filelist.unsorted = False

            files = filelist.files[:]

            # Trim the list
            files, total, start = self.item_count(handler, query,
                                                  handler.cname, files,
                                                  filelist.last_start)
            filelist.last_start = start
        return files, total, start

    def get_playlist(self, handler, query):
        subcname = query['Container'][0]

        url = subcname.find('http://')
        if url >= 0:
            list_name = subcname[url:]
        else:
            list_name = self.get_local_path(handler, query)

        recurse = query.get('Recurse', ['No'])[0] == 'Yes'
        playlist = self.parse_playlist(list_name, recurse)

        # Shuffle?
        if 'Random' in query.get('SortOrder', ['Normal'])[0]:
            seed = query.get('RandomSeed', [''])[0]
            start = query.get('RandomStart', [''])[0]

            shuffle(playlist, seed)
            if start:
                local_base_path = self.get_local_base_path(handler, query)
                start = unquote(start)
                start = start.replace(os.path.sep + handler.cname,
                                      local_base_path, 1)
                move_to_front(playlist, start, handler.server.logger)

        # Trim the list
        return self.item_count(handler, query, handler.cname, playlist)
