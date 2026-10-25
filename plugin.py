import importlib
import logging
import os
import random
import sys
import threading
import time
import unicodedata
from urllib.parse import quote as _quote, unquote_plus

from lrucache import LRUCache

if os.path.sep == '/':
    quote = _quote
    unquote = unquote_plus
else:
    quote = lambda x: _quote(x.replace(os.path.sep, '/'))
    unquote = lambda x: os.path.normpath(unquote_plus(x))

logger = logging.getLogger('pyTivo.plugin')


class Error:
    CONTENT_TYPE = 'text/html'


def GetPlugin(name):
    try:
        module = importlib.import_module('plugins.%s.%s' % (name, name))
        return getattr(module, module.CLASS_NAME)()
    except ImportError:
        logger.exception('Error loading the %s plugin. Check the type '
                         'setting for your share.', name)
        return Error


class FileData:
    def __init__(self, name, isdir):
        self.name = name
        self.isdir = isdir
        st = os.stat(name)
        self.mdate = st.st_mtime
        self.size = st.st_size


class SortList:
    def __init__(self, files):
        self.files = files
        self.unsorted = True
        self.sortby = None
        self.last_start = 0
        self.lock = threading.RLock()


def list_files(path, filter_function=None, recurse=False, file_class=FileData,
               keep_dirs=False):
    """List the visible entries in path (recursively, if asked).

    Directories are included only when not recursing; when recursing, their
    contents are listed instead. filter_function(path) decides which
    entries are kept; keep_dirs=True keeps directories regardless of it.
    """
    files = []
    try:
        names = os.listdir(path)
    except OSError as msg:
        logger.debug('Unable to list %s: %s', path, msg)
        return files

    for name in names:
        if name.startswith('.'):
            continue
        f = os.path.join(path, name)
        isdir = os.path.isdir(f)
        if sys.platform == 'darwin':
            f = unicodedata.normalize('NFC', f)
        if recurse and isdir:
            files.extend(list_files(f, filter_function, recurse, file_class,
                                    keep_dirs))
        elif ((keep_dirs and isdir) or not filter_function or
              filter_function(f)):
            try:
                files.append(file_class(f, isdir))
            except OSError as msg:
                logger.debug('Skipping %s: %s', f, msg)
    return files


def shuffle(items, seed=''):
    """Shuffle in place; a given seed always produces the same order, so
    the TiVo can page through a shuffled list consistently."""
    if seed:
        random.Random(seed).shuffle(items)
    else:
        random.shuffle(items)


def move_to_front(items, name, logger):
    """Move the item with the given name to the start of the list."""
    filenames = [x.name for x in items]
    try:
        index = filenames.index(name)
        items.insert(0, items.pop(index))
    except ValueError:
        logger.warning('Start not found: ' + name)


class Plugin:

    CONTENT_TYPE = ''

    recurse_cache = LRUCache(5)
    dir_cache = LRUCache(10)

    def __new__(cls, *args, **kwds):
        it = cls.__dict__.get('__it__')
        if it is not None:
            return it
        cls.__it__ = it = object.__new__(cls)
        it.init(*args, **kwds)
        return it

    def init(self):
        pass

    def send_file(self, handler, path, query):
        handler.send_content_file(path)

    def get_local_base_path(self, handler, query):
        return os.path.normpath(handler.container['path'])

    def get_local_path(self, handler, query, must_exist=False):
        """Map the requested Container to a local path. Returns False for
        path tricks, or (with must_exist) for a folder that's gone."""
        subcname = query['Container'][0]

        path = self.get_local_base_path(handler, query)
        for folder in subcname.split('/')[1:]:
            if folder == '..':
                return False
            path = os.path.join(path, folder)
        if must_exist and not os.path.exists(path):
            return False
        return path

    def item_count(self, handler, query, cname, files, last_start=0):
        """Return only the desired portion of the list, as specified by
           ItemCount, AnchorItem and AnchorOffset. 'files' is either a
           list of strings, OR a list of objects with a 'name' attribute.
        """
        def no_anchor(handler, anchor):
            handler.server.logger.warning('Anchor not found: ' + anchor)

        totalFiles = len(files)
        index = 0

        if totalFiles and 'ItemCount' in query:
            count = int(query['ItemCount'][0])

            if 'AnchorItem' in query:
                bs = '/TiVoConnect?Command=QueryContainer&Container='
                local_base_path = self.get_local_base_path(handler, query)

                anchor = query['AnchorItem'][0]
                if anchor.startswith(bs):
                    anchor = anchor.replace(bs, '/', 1)
                anchor = unquote(anchor)
                anchor = anchor.replace(os.path.sep + cname, local_base_path, 1)
                if '://' not in anchor:
                    anchor = os.path.normpath(anchor)

                if isinstance(files[0], str):
                    filenames = files
                else:
                    filenames = [x.name for x in files]
                try:
                    index = filenames.index(anchor, last_start)
                except ValueError:
                    if last_start:
                        try:
                            index = filenames.index(anchor, 0, last_start)
                        except ValueError:
                            no_anchor(handler, anchor)
                    else:
                        no_anchor(handler, anchor) # just use index = 0

                if count > 0:
                    index += 1

                if 'AnchorOffset' in query:
                    index += int(query['AnchorOffset'][0])

            if count < 0:
                index = (index + count) % len(files)
                count = -count
            files = files[index:index + count]

        return files, totalFiles, index

    def cached_file_list(self, path, recurse, build, recurse_ttl=None):
        """Return a SortList for path, reusing a cached listing when it is
        still fresh. build(path, recurse) creates a new list of files."""
        filelist = None
        rc = self.recurse_cache
        dc = self.dir_cache
        if recurse:
            if path in rc and (recurse_ttl is None or
                               rc.mtime(path) + recurse_ttl >= time.time()):
                filelist = rc[path]
        else:
            updated = os.path.getmtime(path)
            if path in dc and dc.mtime(path) >= updated:
                filelist = dc[path]
            for p in rc:
                if path.startswith(p) and rc.mtime(p) < updated:
                    del rc[p]

        if not filelist:
            filelist = SortList(build(path, recurse))

            if recurse:
                rc[path] = filelist
            else:
                dc[path] = filelist
        return filelist

    def get_files(self, handler, query, filterFunction=None,
                  force_alpha=False, allow_recurse=True):

        path = self.get_local_path(handler, query)

        file_type = query.get('Filter', [''])[0]

        recurse = allow_recurse and query.get('Recurse', ['No'])[0] == 'Yes'

        def build(path, recurse):
            filt = None
            if filterFunction:
                filt = lambda f: filterFunction(f, file_type)
            return list_files(path, filt, recurse)

        filelist = self.cached_file_list(path, recurse, build, 300)

        sortby = query.get('SortOrder', ['Normal'])[0]
        with filelist.lock:
            if filelist.unsorted or filelist.sortby != sortby:
                if force_alpha:
                    filelist.files.sort(key=lambda x: (not x.isdir, x.name))
                elif sortby == '!CaptureDate':
                    filelist.files.sort(key=lambda x: x.mdate, reverse=True)
                else:
                    filelist.files.sort(key=lambda x: x.name)

                filelist.sortby = sortby
                filelist.unsorted = False

            files = filelist.files[:]

            # Trim the list
            files, total, start = self.item_count(handler, query,
                                                  handler.cname, files,
                                                  filelist.last_start)
            if len(files) > 1:
                filelist.last_start = start
        return files, total, start
