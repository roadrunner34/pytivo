# Photo module for pyTivo by William McBrine <wmcbrine@users.sf.net>
# based partly on music.py and plugin.py
#
# After version 0.15, see git for the history
#
# Version 0.15, Dec. 29 -- allow Unicode; better error messages
# Version 0.14, Dec. 26 -- fix Random sort; handle ItemCount == 0
# Version 0.13, Dec. 19 -- more thread-safe; use draft mode always
# Version 0.12, Dec. 18 -- get date and orientation from Exif
# Version 0.11, Dec. 16 -- handle ItemCount, AnchorItem etc. correctly
# Version 0.10, Dec. 14 -- give full list if no ItemCount; use antialias
#                          mode always; allow larger thumbnails
# Version 0.9,  Dec. 13 -- different sort types
# Version 0.8,  Dec. 12 -- faster thumbnails, better quality full views
# Version 0.7,  Dec. 11 -- fix missing item on thumbnail scroll up,
#                          better anchor and path handling
# Version 0.6,  Dec. 10 -- cache recursive lookups for faster slide shows
# Version 0.5,  Dec. 10 -- fix reboot problem by keeping directory names
#                          (vs. contents) out of "Recurse=Yes" lists
# Version 0.4,  Dec. 10 -- drop the use of playable_cache, add path
#                          separator kludges for Windows
# Version 0.3,  Dec. 8  -- revert to using PixelShape, workaround for
#                          Image.save() under Windows
# Version 0.2,  Dec. 8  -- thumbnail caching, faster thumbnails
# Version 0.1,  Dec. 7, 2007

import logging
import os
import re
import subprocess
import sys
import tempfile
import time
from io import BytesIO
from urllib.parse import unquote_plus
from xml.sax.saxutils import escape

logger = logging.getLogger('pyTivo.photo')

use_pil = True
try:
    from PIL import Image
    try:
        LANCZOS = Image.Resampling.LANCZOS
    except AttributeError:      # Pillow < 9.1
        LANCZOS = Image.LANCZOS
except ImportError:
    use_pil = False
    logger.info('Python Imaging Library not found; using FFmpeg')

import config
from Cheetah.Template import Template
from lrucache import LRUCache
from plugin import (EncodeUnicode, Plugin, quote, unquote, list_files,
                    move_to_front, shuffle)
from plugins.video.transcode import kill

# determine if application is a script file or frozen exe
SCRIPTDIR = os.path.dirname(__file__)
if getattr(sys, 'frozen', False):
    SCRIPTDIR = os.path.join(sys._MEIPASS, 'plugins', 'photo')

CLASS_NAME = 'Photo'

# Match Exif date -- YYYY:MM:DD HH:MM:SS
exif_date = re.compile(r'(\d{4}):(\d\d):(\d\d) (\d\d):(\d\d):(\d\d)').search

# Match Exif orientation, Intel and Motorola versions
exif_orient_i = \
    re.compile(b'\x12\x01\x03\x00\x01\x00\x00\x00(.)\x00\x00\x00',
               re.DOTALL).search
exif_orient_m = \
    re.compile(b'\x01\x12\x00\x03\x00\x00\x00\x01\x00(.)\x00\x00',
               re.DOTALL).search

# Find size in FFmpeg output
ffmpeg_size = re.compile(r'.*Video: .+, (\d+)x(\d+)[, ].*')

# Preload the template
def tmpl(name):
    with open(os.path.join(SCRIPTDIR, 'templates', name),
              encoding='utf-8') as f:
        return f.read()

PHOTO_TEMPLATE = tmpl('container.tmpl')
ITEM_TEMPLATE = tmpl('item.tmpl')

JFIF_TAG = b'\xff\xe0\x00\x10JFIF\x00\x01\x02\x00\x00\x01\x00\x01\x00\x00'

class FileData:
    def __init__(self, name, isdir):
        self.name = name
        self.isdir = isdir
        st = os.stat(name)
        self.cdate = st.st_ctime
        self.mdate = st.st_mtime

class Photo(Plugin):

    CONTENT_TYPE = 'x-container/tivo-photos'

    # LRUCache is thread-safe on its own
    media_data_cache = LRUCache(300)  # info and thumbnails
    recurse_cache = LRUCache(5)       # recursive directory lists
    dir_cache = LRUCache(10)          # non-recursive lists

    def new_size(self, oldw, oldh, width, height, pshape):
        pixw, pixh = [int(x) for x in pshape.split(':')]

        if not width: width = oldw
        if not height: height = oldh

        oldw *= pixh
        oldh *= pixw

        ratio = float(oldw) / oldh

        if float(width) / height < ratio:
            height = int(width / ratio)
        else:
            width = int(height * ratio)

        return width, height

    def parse_exif(self, exif, rot, attrs):
        # Capture date
        if attrs and 'odate' not in attrs:
            date = exif_date(exif.decode('latin-1'))
            if date:
                year, month, day, hour, minute, second = (int(x)
                    for x in date.groups())
                if year:
                    odate = time.mktime((year, month, day, hour,
                                         minute, second, -1, -1, -1))
                    attrs['odate'] = '%#x' % int(odate)

        # Orientation
        if attrs and 'exifrot' in attrs:
            rot = (rot + attrs['exifrot']) % 360
        else:
            if exif[6:7] == b'I':
                orient = exif_orient_i(exif)
            else:
                orient = exif_orient_m(exif)

            if orient:
                exifrot = {
                    1:   0,
                    2:   0,
                    3: 180,
                    4: 180,
                    5:  90,
                    6: -90,
                    7: -90,
                    8:  90}.get(orient.group(1)[0], 0)

                rot = (rot + exifrot) % 360
                if attrs:
                    attrs['exifrot'] = exifrot

        return rot

    def get_image_pil(self, path, width, height, pshape, rot, attrs):
        # Load
        try:
            pic = Image.open(path)
        except Exception as msg:
            return False, 'Could not open %s -- %s' % (path, msg)

        # Set draft mode
        try:
            if width and height:
                pic.draft('RGB', (width, height))
        except Exception as msg:
            return False, 'Failed to set draft mode for %s -- %s' % (path, msg)

        # Read Exif data if possible
        if 'exif' in pic.info:
            rot = self.parse_exif(pic.info['exif'], rot, attrs)

        # Rotate
        try:
            if rot:
                pic = pic.rotate(rot, expand=True)
        except Exception as msg:
            return False, 'Rotate failed on %s -- %s' % (path, msg)

        # De-palletize
        try:
            if pic.mode not in ('RGB', 'L'):
                pic = pic.convert('RGB')
        except Exception as msg:
            return False, 'Palette conversion failed on %s -- %s' % (path, msg)

        # Old size
        oldw, oldh = pic.size

        width, height = self.new_size(oldw, oldh, width, height, pshape)

        try:
            pic = pic.resize((width, height), LANCZOS)
        except Exception as msg:
            return False, 'Resize failed on %s -- %s' % (path, msg)

        # Re-encode
        try:
            out = BytesIO()
            pic.save(out, 'JPEG', quality=85)
            encoded = out.getvalue()
        except Exception as msg:
            return False, 'Encode failed on %s -- %s' % (path, msg)

        return True, encoded

    def run_ffmpeg(self, cmd):
        """Run ffmpeg, honoring the configured time limit. Returns
        (True, stdout bytes, stderr text) or (False, message, '')."""
        # ffmpeg can write more than a pipe buffers, so use files
        with tempfile.TemporaryFile() as out_tmp, \
             tempfile.TemporaryFile() as err_tmp:
            ffmpeg = subprocess.Popen(cmd, stdout=out_tmp, stderr=err_tmp,
                                      stdin=subprocess.DEVNULL)

            # wait configured # of seconds: if ffmpeg is not back give up
            limit = config.getFFmpegWait()
            try:
                ffmpeg.wait(timeout=limit or None)
            except subprocess.TimeoutExpired:
                kill(ffmpeg)
                return False, 'FFmpeg timed out', ''

            out_tmp.seek(0)
            err_tmp.seek(0)
            return (True, out_tmp.read(),
                    err_tmp.read().decode('utf-8', 'replace'))

    def get_size_ffmpeg(self, ffmpeg_path, fname):
        status, _, output = self.run_ffmpeg([ffmpeg_path, '-i', fname])
        if not status:
            return False, _

        x = ffmpeg_size.search(output)
        if x:
            width = int(x.group(1))
            height = int(x.group(2))
        else:
            return False, "Couldn't parse size"

        return True, (width, height)

    def get_image_ffmpeg(self, path, width, height, pshape, rot, attrs):
        ffmpeg_path = config.get_bin('ffmpeg')
        if not ffmpeg_path:
            return False, 'FFmpeg not found'

        fname = path

        if attrs and 'size' in attrs:
            result = attrs['size']
        else:
            status, result = self.get_size_ffmpeg(ffmpeg_path, fname)
            if not status:
                return False, result
            if attrs:
                attrs['size'] = result

        if rot in (90, 270):
            oldh, oldw = result
        else:
            oldw, oldh = result

        width, height = self.new_size(oldw, oldh, width, height, pshape)

        if rot == 270:
            filters = 'transpose=1,'
        elif rot == 180:
            filters = 'hflip,vflip,'
        elif rot == 90:
            filters = 'transpose=2,'
        else:
            filters = ''

        filters += 'format=yuvj420p,'

        neww, newh = oldw, oldh
        while (neww // width >= 50) or (newh // height >= 50):
            neww //= 2
            newh //= 2
            filters += 'scale=%d:%d,' % (neww, newh)

        filters += 'scale=%d:%d' % (width, height)

        cmd = [ffmpeg_path, '-i', fname, '-vf', filters, '-f', 'mjpeg', '-']
        status, output, _ = self.run_ffmpeg(cmd)
        if not status:
            return False, output
        if not output:
            return False, 'FFmpeg produced no image'

        if b'JFIF' not in output[:10]:
            output = output[:2] + JFIF_TAG + output[2:]

        return True, output

    def send_file(self, handler, path, query):

        def send_jpeg(data):
            handler.send_fixed(data, 'image/jpeg')

        if 'Format' in query and query['Format'][0] != 'image/jpeg':
            handler.send_error(415)
            return

        attrs = self.media_data_cache.get(path)

        # Set rotation
        if attrs:
            rot = attrs['rotation']
        else:
            rot = 0

        if 'Rotation' in query:
            rot = (rot - int(query['Rotation'][0])) % 360
            if attrs:
                attrs['rotation'] = rot
                if 'thumb' in attrs:
                    del attrs['thumb']

        # Requested size
        width = int(query.get('Width', ['0'])[0])
        height = int(query.get('Height', ['0'])[0])

        # Return saved thumbnail?
        if attrs and 'thumb' in attrs and 0 < width < 100 and 0 < height < 100:
            send_jpeg(attrs['thumb'])
            return

        # Requested pixel shape
        pshape = query.get('PixelShape', ['1:1'])[0]

        # Build a new image
        if use_pil:
            status, result = self.get_image_pil(path, width, height,
                                                pshape, rot, attrs)
        else:
            status, result = self.get_image_ffmpeg(path, width, height,
                                                   pshape, rot, attrs)

        if status:
            # Save thumbnails
            if attrs and width < 100 and height < 100:
                attrs['thumb'] = result

            # Send it
            send_jpeg(result)
        else:
            handler.server.logger.error(result)
            handler.send_error(404)

    def QueryContainer(self, handler, query):

        # Reject a malformed request -- these attributes should only
        # appear in requests to send_file, but sometimes appear here
        badattrs = ('Rotation', 'Width', 'Height', 'PixelShape')
        for i in badattrs:
            if i in query:
                handler.send_error(404)
                return

        local_base_path = self.get_local_base_path(handler, query)
        if not self.get_local_path(handler, query):
            handler.send_error(404)
            return

        def ImageFileFilter(f):
            goodexts = ('.jpg', '.gif', '.png', '.bmp', '.tif', '.xbm',
                        '.xpm', '.pgm', '.pbm', '.ppm', '.pcx', '.tga',
                        '.fpx', '.ico', '.pcd', '.jpeg', '.tiff', '.nef')
            return os.path.splitext(f)[1].lower() in goodexts

        def media_data(f):
            if f.name in self.media_data_cache:
                return self.media_data_cache[f.name]

            item = {}
            item['path'] = f.name
            item['part_path'] = f.name.replace(local_base_path, '', 1)
            item['name'] = os.path.basename(f.name)
            item['is_dir'] = f.isdir
            item['rotation'] = 0
            item['cdate'] = '%#x' % int(f.cdate)
            item['mdate'] = '%#x' % int(f.mdate)

            self.media_data_cache[f.name] = item
            return item

        t = Template(PHOTO_TEMPLATE, filter=EncodeUnicode)
        t.name = query['Container'][0]
        t.container = handler.cname
        t.files, t.total, t.start = self.get_files(handler, query,
            ImageFileFilter)
        t.files = [media_data(f) for f in t.files]
        t.quote = quote
        t.escape = escape

        handler.send_xml(str(t))

    def QueryItem(self, handler, query):
        splitpath = [x for x in unquote_plus(query['Url'][0]).split('/') if x]
        path = os.path.join(handler.container['path'], *splitpath[1:])

        if path in self.media_data_cache:
            t = Template(ITEM_TEMPLATE, filter=EncodeUnicode)
            t.file = self.media_data_cache[path]
            t.escape = escape
            handler.send_xml(str(t))
        else:
            handler.send_error(404)

    def get_files(self, handler, query, filterFunction):

        path = self.get_local_path(handler, query)

        # Build the list
        recurse = query.get('Recurse', ['No'])[0] == 'Yes'

        def build(path, recurse):
            return list_files(path, filterFunction, recurse, FileData,
                              keep_dirs=True)

        filelist = self.cached_file_list(path, recurse, build)

        filelist.lock.acquire()

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

        if filelist.unsorted or filelist.sortby != sortby:
            if 'Random' in sortby:
                shuffle(filelist.files, seed)
                if start:
                    local_base_path = self.get_local_base_path(handler, query)
                    start = unquote(start)
                    start = start.replace(os.path.sep + handler.cname,
                                          local_base_path, 1)
                    move_to_front(filelist.files, start,
                                  handler.server.logger)
            else:
                if 'CaptureDate' in sortby:
                    sortkey = lambda x: x.cdate
                elif 'LastChangeDate' in sortby:
                    sortkey = lambda x: x.mdate
                else:
                    sortkey = lambda x: x.name

                if 'Type' in sortby:
                    # Directories first
                    filelist.files.sort(key=lambda x: (not x.isdir,
                                                       sortkey(x)))
                else:
                    filelist.files.sort(key=sortkey)

            filelist.sortby = sortby
            filelist.unsorted = False

        files = filelist.files[:]

        # Filter it -- this section needs work
        if 'Filter' in query:
            usedir = 'folder' in query['Filter'][0]
            useimg = 'image' in query['Filter'][0]
            if not usedir:
                files = [x for x in files if not x.isdir]
            elif usedir and not useimg:
                files = [x for x in files if x.isdir]

        files, total, start = self.item_count(handler, query, handler.cname,
                                              files, filelist.last_start)
        filelist.last_start = start
        filelist.lock.release()
        return files, total, start
