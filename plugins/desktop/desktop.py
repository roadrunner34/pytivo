import os
import sys
import logging
from urllib import unquote_plus
from asar import AsarArchive

from plugin import Plugin
logger = logging.getLogger('pyTivo.desktop')

# This plugin only runs in forzen mode
SCRIPTDIR = ''
if getattr(sys, 'frozen', False):
    SCRIPTDIR = os.path.join(sys._MEIPASS, 'plugins', 'desktop')

CLASS_NAME = 'Desktop'

class Desktop(Plugin):
    CONTENT_TYPE = 'text/html'

    def send_file(self, handler, path, query):
        if getattr(sys, 'frozen', False):
            self.expand_files()
            if os.path.isfile(path):
                handler.send_content_file(path)
            else:
                content = os.path.normpath(os.path.join(SCRIPTDIR, 'content'))
                index = os.path.join(content, 'index.html')
                handler.send_content_file(index)
        else:
            handler.send_error(404)


    def expand_files(self):
        if getattr(sys, 'frozen', False):
            asar_file = os.path.join(os.path.dirname(sys.executable), 'desktop', 'resources', 'app.asar')
            if sys.platform == 'darwin':
                exedir = os.path.join(os.path.dirname(os.path.dirname(os.path.dirname(os.path.dirname(sys.executable)))))  # on Mac pyTivo is inside a .app bundle
                asar_file = os.path.join(exedir, 'pyTivoDesktop.app', 'contents', 'resources', 'app.asar')

            try:
                with AsarArchive.open(asar_file) as archive:
                    archive.extract(os.path.join(SCRIPTDIR, 'content'))

                index = file(os.path.join(SCRIPTDIR, 'content', 'index.html'), 'rb').read()
                index = index.replace('./', '/Desktop/')
                file(os.path.join(SCRIPTDIR, 'content', 'index.html'), 'w').write(index)

                return True
            except:
                return False
