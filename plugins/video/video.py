import calendar
import logging
import os
import sys
import re
import struct
import thread
import time
import urllib
import zlib
import json
import subprocess
from UserDict import DictMixin
from datetime import datetime, timedelta
from xml.sax.saxutils import escape

from Cheetah.Template import Template
from lrucache import LRUCache

import config
import metadata
import transcode
from plugin import EncodeUnicode, Plugin, quote

logger = logging.getLogger('pyTivo.video.video')

# determine if application is a script file or frozen exe
SCRIPTDIR = os.path.dirname(__file__)
if getattr(sys, 'frozen', False):
    SCRIPTDIR = os.path.join(sys._MEIPASS, 'plugins', 'video')

CLASS_NAME = 'Video'

# Preload the templates
def tmpl(name):
    return file(os.path.join(SCRIPTDIR, 'templates', name), 'rb').read()

XML_CONTAINER_TEMPLATE = tmpl('container_xml.tmpl')
TVBUS_TEMPLATE = tmpl('TvBus.tmpl')

EXTENSIONS = """.tivo .mpg .avi .wmv .mov .flv .f4v .vob .mp4 .m4v .mkv
.ts .tp .trp .3g2 .3gp .3gp2 .3gpp .amv .asf .avs .bik .bix .box .bsf
.dat .dif .divx .dmb .dpg .dv .dvr-ms .evo .eye .flc .fli .flx .gvi .ivf
.m1v .m21 .m2t .m2ts .m2v .m2p .m4e .mjp .mjpeg .mod .moov .movie .mp21
.mpe .mpeg .mpv .mpv2 .mqv .mts .mvb .nsv .nuv .nut .ogm .qt .rm .rmvb
.rts .scm .smv .ssm .svi .vdo .vfw .vid .viv .vivo .vp6 .vp7 .vro .webm
.wm .wmd .wtv .yuv""".split()

LIKELYTS = """.ts .tp .trp .3g2 .3gp .3gp2 .3gpp .m2t .m2ts .mts .mp4
.m4v .flv .mkv .mov .wtv .dvr-ms .webm""".split()

status = {} # Global variable to track uploads

use_extensions = True
try:
    assert(config.get_bin('ffmpeg'))
except:
    use_extensions = False

def uniso(iso):
    return time.strptime(iso[:19], '%Y-%m-%dT%H:%M:%S')

def isodt(iso):
    return datetime(*uniso(iso)[:6])

def isogm(iso):
    return int(calendar.timegm(uniso(iso)))

class Video(Plugin):

    CONTENT_TYPE = 'x-container/tivo-videos'

    tvbus_cache = LRUCache(1)

    def video_file_filter(self, full_path, type=None):
        if os.path.isdir(unicode(full_path, 'utf-8')):
            return True
        if use_extensions:
            return os.path.splitext(full_path)[1].lower() in EXTENSIONS
        else:
            return transcode.supported_format(full_path)

    def GetActiveTransferCount(self, handler, query):
        global status
        json_config = {}
        count = 0
        for tivo in status:
            for file in status[tivo]:
                if status[tivo][file]['active']:
                    count += 1

        json_config['count'] = count
        handler.send_json(json.dumps(json_config))

    def GetTransferStatus(selfself, handler, query):
        global status
        handler.send_json(json.dumps(status))

    def cleanup_status(self):
        global status

        now = time.time()
        for tivo in status.keys():
            for file in status[tivo].keys():
                if not status[tivo][file]['active']:
                    elapsed = now - status[tivo][file]['end']
                    if elapsed >= 86400: # 86400 = one day
                        del status[tivo][file]

            if len(status[tivo]) < 1:
                del status[tivo]


    def send_file(self, handler, path, query):
        global status

        self.cleanup_status() # Keep status object from getting too big

        mime = 'video/x-tivo-mpeg'
        tsn = handler.headers.getheader('tsn', '')

        try:
            assert(tsn)
            tivo_name = config.tivos[tsn].get('name', tsn)
        except:
            tivo_name = handler.address_string()

        if not tivo_name in status:
            status[tivo_name] = {}

        is_tivo_file = False
        tivo_header_size = 0
        is_tivo_ts = False

        try:
            f = open(unicode(path, 'utf-8'), 'rb')
            tivo_header = bytearray(f.read(16))
            if tivo_header[0:4].decode("utf-8") == 'TiVo':
                is_tivo_file = True
                try:
                    if (tivo_header[7] & 0x20 != 0):
                        is_tivo_ts = True
                except Exception, msg:
                    test = 0
                tivo_header_size = struct.unpack_from('>L', tivo_header, 10)[0]
            f.close()
        except:
            pass

        tivo_mak = config.get_tsn('tivo_mak', tsn)
        has_tivolibre = bool(config.get_bin('tivolibre'))
        has_tivodecode = bool(config.get_bin('tivodecode'))

        use_tivolibre = False
        if has_tivolibre and bool(config.get_server('tivolibre_upload', True)):
            use_tivolibre = True

        if 'Format' in query:
            mime = query['Format'][0]

        needs_converion = (((is_tivo_file and is_tivo_ts) or (is_tivo_file and not has_tivolibre)) and mime == 'video/mpeg')
        compatible = (not needs_converion and
                      transcode.tivo_compatible(path, tsn, mime)[0])

        try:  # "bytes=XXX-"
            offset = int(handler.headers.getheader('Range')[6:-1])
        except:
            offset = 0

        if needs_converion:
            valid = bool((has_tivodecode or has_tivolibre) and tivo_mak)
        else:
            valid = True

        if valid and offset:
            valid = ((compatible and offset < os.path.getsize(path)) or
                     (not compatible and transcode.is_resumable(path, offset)))

            if status[tivo_name][path]:
                valid = (offset != status[tivo_name][path]['offset']) # Don't let the TiVo loop over and over in the same spot
                status[tivo_name][path]['error'] = 'Repeat offset call'

        #faking = (mime in ['video/x-tivo-mpeg-ts', 'video/x-tivo-mpeg'] and
        faking = (mime == 'video/x-tivo-mpeg' and
                  not (is_tivo_file and compatible))
        fname = unicode(path, 'utf-8')
        thead = ''
        if faking:
            thead = self.tivo_header(tsn, path, mime)

        size = os.path.getsize(fname) + len(thead)
        if compatible:
            handler.send_response(206)
            handler.send_header('Content-Length', size - offset)
            handler.send_header('Content-Range', 'bytes %d-%d/%d' % 
                                (offset, size - offset - 1, size))
        else:
            handler.send_response(206)
            handler.send_header('Transfer-Encoding', 'chunked')
        handler.send_header('Content-Type', mime)
        handler.end_headers()

        logger.info('[%s] Start sending "%s" to %s' %
                    (time.strftime('%d/%b/%Y %H:%M:%S'), fname, tivo_name))

        if valid:
            start_time = time.time()
            last_interval = start_time
            now = start_time
            count = 0
            output = 0

            if not path in status[tivo_name]:
                status[tivo_name][path] = {'active': True, 'decrypting': False, 'transcoding': False, 'offset': offset,
                                     'start': start_time, 'end': start_time, 'rate': 0, 'size': size, 'output': 0, 'error': '' }
            else:
                status[tivo_name][path]['active'] = True
                status[tivo_name][path]['offset'] = offset

            if compatible:
                logger.debug('"%s" is tivo compatible' % fname)
                f = open(fname, 'rb')
                tivolibre = None
                if not offset:
                    if faking:
                        handler.wfile.write(thead)
                        count += len(thead)
                        output += len(thead)
                    elif tivo_header_size > 0:
                        block = f.read(tivo_header_size)
                        handler.wfile.write(block)
                        count += len(block)
                        output += len(block)
                try:
                    if is_tivo_file and use_tivolibre:
                        status[tivo_name][path]['decrypting'] = True

                        f.close()
                        tivolibre_path = config.get_bin('tivolibre')
                        tcmd = [tivolibre_path, '-m', tivo_mak, '-i', fname]
                        tivolibre = subprocess.Popen(tcmd, stdout=subprocess.PIPE, bufsize=(512 * 1024))
                        f = tivolibre.stdout

                    if offset:
                        if tivolibre:
                            raise Exception('tivolibre does not support offset')
                        offset -= len(thead)
                        f.seek(offset)

                    while True:
                        block = f.read(512 * 1024)
                        if not block:
                            break
                        handler.wfile.write(block)
                        count += len(block)
                        output += len(block)

                        now = time.time()
                        elapsed = now - last_interval
                        if elapsed >= 1:
                            status[tivo_name][path]['rate'] = (count * 8.0) / elapsed
                            status[tivo_name][path]['output'] += count
                            count = 0
                            last_interval = now

                    if tivolibre:
                        tivolibre.wait()

                except Exception, msg:
                    status[tivo_name][path]['error'] = str(msg)
                    if tivolibre:
                        tivolibre.kill()
                        tivolibre.wait()

                    logger.info(msg)

                f.close()
            else:
                status[tivo_name][path]['transcoding'] = True
                logger.debug('"%s" is not tivo compatible' % fname)
                if offset:
                    count = transcode.resume_transfer(path, handler.wfile,
                                                      offset, status[tivo_name][path])
                else:
                    count = transcode.transcode(False, path, handler.wfile, status[tivo_name][path],
                                                is_tivo_file, tsn, mime, thead)

            end_time = time.time()
            elapsed = end_time - status[tivo_name][path]['start']
            rate = count * 8.0 / elapsed

            status[tivo_name][path]['active'] = False
            status[tivo_name][path]['end'] = end_time
            status[tivo_name][path]['rate'] = rate

            logger.info('[%s] Done sending "%s" to %s, %d bytes, %.2f Mb/s' %
                        (time.strftime('%d/%b/%Y %H:%M:%S'), fname,
                         tivo_name, count, rate*1024*1024))

        else:
            logger.info('Invalid file "%s" requested by %s' % fname, tivo_name)

        try:
            if not compatible:
                handler.wfile.write('0\r\n\r\n')
            handler.wfile.flush()
        except Exception, msg:
            logger.info(msg)


    def __duration(self, full_path):
        return transcode.video_info(full_path)['millisecs']

    def __total_items(self, full_path):
        count = 0
        try:
            full_path = unicode(full_path, 'utf-8')
            for f in os.listdir(full_path):
                if f.startswith('.'):
                    continue
                f = os.path.join(full_path, f)
                f2 = f.encode('utf-8')
                if os.path.isdir(f):
                    count += 1
                elif use_extensions:
                    if os.path.splitext(f2)[1].lower() in EXTENSIONS:
                        count += 1
                elif f2 in transcode.info_cache:
                    if transcode.supported_format(f2):
                        count += 1
        except:
            pass
        return count

    def __est_size(self, full_path, tsn='', mime=''):
        # Size is estimated by taking audio and video bit rate adding 2%

        if transcode.tivo_compatible(full_path, tsn, mime)[0]:
            return os.path.getsize(unicode(full_path, 'utf-8'))
        else:
            # Must be re-encoded
            audioBPS = config.getMaxAudioBR(tsn) * 1000
            #audioBPS = config.strtod(config.getAudioBR(tsn))
            videoBPS = transcode.select_videostr(full_path, tsn)
            bitrate =  audioBPS + videoBPS
            return int((self.__duration(full_path) / 1000) *
                       (bitrate * 1.02 / 8))

    def metadata_full(self, full_path, tsn='', mime='', mtime=None):
        data = {}
        vInfo = transcode.video_info(full_path)

        if ((int(vInfo['vHeight']) >= 720 and
             config.getTivoHeight >= 720) or
            (int(vInfo['vWidth']) >= 1280 and
             config.getTivoWidth >= 1280)):
            data['showingBits'] = '4096'

        data.update(metadata.basic(full_path, mtime))
        if full_path[-5:].lower() == '.tivo':
            data.update(metadata.from_tivo(full_path))
        if full_path[-4:].lower() == '.wtv':
            data.update(metadata.from_mscore(vInfo['rawmeta']))

        if 'episodeNumber' in data:
            try:
                ep = int(data['episodeNumber'])
            except:
                ep = 0
            data['episodeNumber'] = str(ep)

        if config.getDebug() and 'vHost' not in data:
            compatible, reason = transcode.tivo_compatible(full_path, tsn, mime)
            if compatible:
                transcode_options = []
            else:
                transcode_options = transcode.transcode(True, full_path,
                                                        '', None, False, tsn, mime)
            data['vHost'] = (
                ['TRANSCODE=%s, %s' % (['YES', 'NO'][compatible], reason)] +
                ['SOURCE INFO: '] +
                ["%s=%s" % (k, v)
                 for k, v in sorted(vInfo.items(), reverse=True)] +
                ['TRANSCODE OPTIONS: '] +
                transcode_options +
                ['SOURCE FILE: ', os.path.basename(full_path)]
            )

        now = datetime.utcnow()
        if 'time' in data:
            if data['time'].lower() == 'file':
                if not mtime:
                    mtime = os.path.getmtime(unicode(full_path, 'utf-8'))
                try:
                    now = datetime.utcfromtimestamp(mtime)
                except:
                    logger.warning('Bad file time on ' + full_path)
            elif data['time'].lower() == 'oad':
                    now = isodt(data['originalAirDate'])
            else:
                try:
                    now = isodt(data['time'])
                except:
                    logger.warning('Bad time format: ' + data['time'] +
                                   ' , using current time')

        duration = self.__duration(full_path)
        duration_delta = timedelta(milliseconds = duration)
        min = duration_delta.seconds / 60
        sec = duration_delta.seconds % 60
        hours = min / 60
        min = min % 60

        data.update({'time': now.isoformat(),
                     'startTime': now.isoformat(),
                     'stopTime': (now + duration_delta).isoformat(),
                     'size': self.__est_size(full_path, tsn, mime),
                     'duration': duration,
                     'iso_duration': ('P%sDT%sH%sM%sS' % 
                          (duration_delta.days, hours, min, sec))})

        return data

    def QueryContainer(self, handler, query):
        tsn = handler.headers.getheader('tsn', '')
        subcname = query['Container'][0]

        if not self.get_local_path(handler, query):
            handler.send_error(404)
            return

        container = handler.container
        force_alpha = container.getboolean('force_alpha')
        ar = container.get('allow_recurse', 'auto').lower()
        if ar == 'auto':
            allow_recurse = not tsn or tsn[0] < '7'
        else:
            allow_recurse = ar in ('1', 'yes', 'true', 'on')

        files, total, start = self.get_files(handler, query,
                                             self.video_file_filter,
                                             force_alpha, allow_recurse)

        videos = []
        local_base_path = self.get_local_base_path(handler, query)
        for f in files:
            video = VideoDetails()
            mtime = f.mdate
            try:
                ltime = time.localtime(mtime)
            except:
                logger.warning('Bad file time on ' + unicode(f.name, 'utf-8'))
                mtime = time.time()
                ltime = time.localtime(mtime)
            video['captureDate'] = hex(int(mtime))
            video['textDate'] = time.strftime('%b %d, %Y', ltime)
            video['name'] = os.path.basename(f.name)
            video['path'] = f.name
            video['part_path'] = f.name.replace(local_base_path, '', 1)
            if not video['part_path'].startswith(os.path.sep):
                video['part_path'] = os.path.sep + video['part_path']
            video['title'] = os.path.basename(f.name)
            video['is_dir'] = f.isdir
            if video['is_dir']:
                video['small_path'] = subcname + '/' + video['name']
                video['total_items'] = self.__total_items(f.name)
            else:
                if len(files) == 1 or f.name in transcode.info_cache:
                    video['valid'] = transcode.supported_format(f.name)
                    if video['valid']:
                        video.update(self.metadata_full(f.name, tsn,
                                     mtime=mtime))
                        if len(files) == 1:
                            video['captureDate'] = hex(isogm(video['time']))
                else:
                    video['valid'] = True
                    video.update(metadata.basic(f.name, mtime))

                if self.use_ts(tsn, f.name):
                    video['mime'] = 'video/x-tivo-mpeg-ts'
                else:
                    video['mime'] = 'video/x-tivo-mpeg'

                video['textSize'] = metadata.human_size(f.size)

            videos.append(video)

        t = Template(XML_CONTAINER_TEMPLATE, filter=EncodeUnicode)
        t.container = handler.cname
        t.name = subcname
        t.total = total
        t.start = start
        t.videos = videos
        t.quote = quote
        t.escape = escape
        t.crc = zlib.crc32
        t.guid = config.getGUID()
        t.tivos = config.tivos
        handler.send_xml(str(t))

    def use_ts(self, tsn, file_path):
        if config.is_ts_capable(tsn):
            ext = os.path.splitext(file_path)[1].lower()
            if ext == '.tivo':
                try:
                    flag = file(file_path).read(8)
                except:
                    return False
                if ord(flag[7]) & 0x20:
                    return True
            else:
                opt = config.get_ts_flag()
                if ((opt == 'auto' and ext in LIKELYTS) or
                    (opt in ['true', 'yes', 'on'])):
                    return True

        return False

    def get_details_xml(self, tsn, file_path):
        if (tsn, file_path) in self.tvbus_cache:
            details = self.tvbus_cache[(tsn, file_path)]
        else:
            file_info = VideoDetails()
            file_info['valid'] = transcode.supported_format(file_path)
            if file_info['valid']:
                file_info.update(self.metadata_full(file_path, tsn))

            t = Template(TVBUS_TEMPLATE, filter=EncodeUnicode)
            t.video = file_info
            t.escape = escape
            t.get_tv = metadata.get_tv
            t.get_mpaa = metadata.get_mpaa
            t.get_stars = metadata.get_stars
            t.get_color = metadata.get_color
            details = str(t)
            self.tvbus_cache[(tsn, file_path)] = details
        return details

    def tivo_header(self, tsn, path, mime):
        def pad(length, align):
            extra = length % align
            if extra:
                extra = align - extra
            return extra

        if mime == 'video/x-tivo-mpeg-ts':
            flag = 45
        else:
            flag = 13
        details = self.get_details_xml(tsn, path)
        ld = len(details)
        chunk = details + '\0' * (pad(ld, 4) + 4)
        lc = len(chunk)
        blocklen = lc * 2 + 40
        padding = pad(blocklen, 1024)

        return ''.join(['TiVo', struct.pack('>HHHLH', 4, flag, 0, 
                                            padding + blocklen, 2),
                        struct.pack('>LLHH', lc + 12, ld, 1, 0),
                        chunk,
                        struct.pack('>LLHH', lc + 12, ld, 2, 0),
                        chunk, '\0' * padding])

    def TVBusQuery(self, handler, query):
        tsn = handler.headers.getheader('tsn', '')
        f = query['File'][0]
        path = self.get_local_path(handler, query)
        file_path = os.path.normpath(path + '/' + f)

        details = self.get_details_xml(tsn, file_path)

        handler.send_xml(details)

class VideoDetails(DictMixin):

    def __init__(self, d=None):
        if d:
            self.d = d
        else:
            self.d = {}

    def __getitem__(self, key):
        if key not in self.d:
            self.d[key] = self.default(key)
        return self.d[key]

    def __contains__(self, key):
        return True

    def __setitem__(self, key, value):
        self.d[key] = value

    def __delitem__(self, key):
        del self.d[key]

    def keys(self):
        return self.d.keys()

    def __iter__(self):
        return self.d.__iter__()

    def iteritems(self):
        return self.d.iteritems()

    def default(self, key):
        defaults = {
            'showingBits' : '0',
            'displayMajorNumber' : '0',
            'displayMinorNumber' : '0',
            'isEpisode' : 'true',
            'colorCode' : '4',
            'showType' : ('SERIES', '5')
        }
        if key in defaults:
            return defaults[key]
        elif key.startswith('v'):
            return []
        else:
            return ''
