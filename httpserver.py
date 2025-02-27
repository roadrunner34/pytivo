import BaseHTTPServer
import SocketServer
import cgi
import gzip
import logging
import mimetypes
import os
import sys
import shutil
import socket
import time
from cStringIO import StringIO
from email.utils import formatdate
from urllib import unquote_plus, quote
from xml.sax.saxutils import escape

from Cheetah.Template import Template
import config
from plugin import GetPlugin, EncodeUnicode

# determine if application is a script file or frozen exe
SCRIPTDIR = os.path.dirname(__file__)
if getattr(sys, 'frozen', False):
    SCRIPTDIR = sys._MEIPASS

SERVER_INFO = """<?xml version="1.0" encoding="utf-8"?>
<TiVoServer>
<Version>1.6.26</Version>
<InternalName>pyTivo</InternalName>
<InternalVersion>pyTivo Desktop</InternalVersion>
<Organization>pyTivo Developers</Organization>
<Comment>http://www.pyTivoDesktop.com/</Comment>
</TiVoServer>"""

VIDEO_FORMATS = """<?xml version="1.0" encoding="utf-8"?>
<TiVoFormats>
<Format><ContentType>video/x-tivo-mpeg</ContentType><Description/></Format>
</TiVoFormats>"""

VIDEO_FORMATS_TS = """<?xml version="1.0" encoding="utf-8"?>
<TiVoFormats>
<Format><ContentType>video/x-tivo-mpeg</ContentType><Description/></Format>
<Format><ContentType>video/x-tivo-mpeg-ts</ContentType><Description/></Format>
</TiVoFormats>"""

BASE_HTML = """<!DOCTYPE HTML PUBLIC "-//W3C//DTD HTML 4.01//EN"
"http://www.w3.org/TR/html4/strict.dtd">
<html> <head><title>pyTivo</title>
<link rel="stylesheet" type="text/css" href="/main.css">
</head> <body> %s </body> </html>"""

RELOAD = '<p>The <a href="%s">page</a> will reload in %d seconds.</p>'
UNSUP = '<h3>Unsupported Command</h3> <p>Query:</p> <ul>%s</ul>'

class TivoHTTPServer(SocketServer.ThreadingMixIn, BaseHTTPServer.HTTPServer):
    def __init__(self, server_address, RequestHandlerClass):
        self.containers = {}
        self.stop = False
        self.restart = False
        self.logger = logging.getLogger('pyTivo')
        BaseHTTPServer.HTTPServer.__init__(self, server_address, RequestHandlerClass)
        self.daemon_threads = True

    def add_container(self, name, settings):
        if name in self.containers or name == 'TiVoConnect':
            raise Exception('Container Name in use')
        try:
            self.containers[name] = settings
        except KeyError:
            self.logger.error('Unable to add container ' + name)

    def reset(self):
        self.containers.clear()
        for section, settings in config.getShares():
            self.add_container(section, settings)

    def handle_error(self, request, client_address):
        self.logger.exception('Exception during request from %s' % 
                              (client_address,))

    def set_beacon(self, beacon):
        self.beacon = beacon

    def set_service_status(self, status):
        self.in_service = status

class TivoHTTPHandler(BaseHTTPServer.BaseHTTPRequestHandler):
    def __init__(self, request, client_address, server):
        self.wbufsize = 0x10000
        self.server_version = 'pyTivo/1.0'
        self.protocol_version = 'HTTP/1.1'
        self.sys_version = ''

        try:
            BaseHTTPServer.BaseHTTPRequestHandler.__init__(self, request, client_address, server)
        except Exception as msg:
            self.server.logger.info(msg)

    def setup(self):
        BaseHTTPServer.BaseHTTPRequestHandler.setup(self)
        self.request.settimeout(180) # This allows pyTivo to die when user selects Stop Transfer on the TiVo

    def address_string(self):
        host, port = self.client_address[:2]
        return host

    def version_string(self):
        """ Override version_string() so it doesn't include the Python 
            version.

        """
        return self.server_version

    def do_GET(self):
        tsn = self.headers.getheader('TiVo_TCD_ID',
                                     self.headers.getheader('tsn', ''))
        if not self.authorize(tsn):
            return

        if tsn and (not config.tivos_found or tsn in config.tivos):
            attr = config.tivos.get(tsn, {})
            if 'address' not in attr:
                attr['address'] = self.address_string()
            if 'name' not in attr:
                attr['name'] = self.server.beacon.get_name(attr['address'])
            config.tivos[tsn] = attr

        if '?' in self.path:
            path, opts = self.path.split('?', 1)
            query = cgi.parse_qs(opts)
        else:
            path = self.path
            query = {}

        if path == '/TiVoConnect':
            self.handle_query(query, tsn)
        else:
            ## Get File
            splitpath = [x for x in unquote_plus(path).split('/') if x]
            if splitpath:
                self.handle_file(query, splitpath)
            else:
                ## Not a file not a TiVo command
                self.infopage()

    def do_POST(self):
        tsn = self.headers.getheader('TiVo_TCD_ID',
                                     self.headers.getheader('tsn', ''))
        if not self.authorize(tsn):
            return
        ctype, pdict = cgi.parse_header(self.headers.getheader('content-type'))
        if ctype == 'multipart/form-data':
            query = cgi.parse_multipart(self.rfile, pdict)
        else:
            length = int(self.headers.getheader('content-length'))
            qs = self.rfile.read(length)
            query = cgi.parse_qs(qs, keep_blank_values=1)
        self.handle_query(query, tsn)

    def do_command(self, query, command, target, tsn):
        for name, container in config.getShares(tsn):
            if target == name:
                plugin = GetPlugin(container['type'])
                if hasattr(plugin, command):
                    self.cname = name
                    self.container = container
                    method = getattr(plugin, command)
                    method(self, query)
                    return True
                else:
                    break
        return False

    def handle_query(self, query, tsn):
        mname = False
        if 'Command' in query and len(query['Command']) >= 1:

            command = query['Command'][0]

            # If we are looking at the root container
            if (command == 'QueryContainer' and
                (not 'Container' in query or query['Container'][0] == '/')):
                self.root_container()
                return

            if 'Container' in query:
                # Dispatch to the container plugin
                basepath = query['Container'][0].split('/')[0]
                if self.do_command(query, command, basepath, tsn):
                    return

            elif command == 'QueryItem':
                path = query.get('Url', [''])[0]
                splitpath = [x for x in unquote_plus(path).split('/') if x]
                if splitpath and not '..' in splitpath:
                    if self.do_command(query, command, splitpath[0], tsn):
                        return

            elif (command == 'QueryFormats' and 'SourceFormat' in query and
                  query['SourceFormat'][0].startswith('video')):
                if config.is_ts_capable(tsn):
                    self.send_xml(VIDEO_FORMATS_TS)
                else:
                    self.send_xml(VIDEO_FORMATS)
                return

            elif command == 'QueryServer':
                self.send_xml(SERVER_INFO)
                return

            elif command in ('GetActiveTransferCount', 'GetTransferStatus'):
                plugin = GetPlugin('video')
                if hasattr(plugin, command):
                    method = getattr(plugin, command)
                    method(self, query)
                    return True

            elif command in ('FlushServer', 'ResetServer'):
                # Does nothing -- included for completeness
                self.send_response(200)
                self.send_header('Content-Length', '0')
                self.end_headers()
                self.wfile.flush()
                return

        # If we made it here it means we couldn't match the request to
        # anything.
        self.unsupported(query)

    def send_content_file(self, path):
        lmdate = os.path.getmtime(path)
        try:
            handle = open(path, 'rb')
        except:
            self.send_error(404)
            return

        # Send the header
        mime = mimetypes.guess_type(path)[0]
        self.send_response(200)
        if mime:
            self.send_header('Content-Type', mime)
        self.send_header('Content-Length', os.path.getsize(path))
        self.send_header('Last-Modified', formatdate(lmdate))
        self.end_headers()

        # Send the body of the file
        try:
            shutil.copyfileobj(handle, self.wfile)
        except:
            pass
        handle.close()
        self.wfile.flush()

    def handle_file(self, query, splitpath):
        if '..' not in splitpath:    # Protect against path exploits
            ## Pass it off to a plugin?
            for name, container in self.server.containers.items():
                if splitpath[0] == name:
                    self.cname = name
                    self.container = container
                    base = os.path.normpath(container['path'])
                    path = os.path.join(base, *splitpath[1:])
                    plugin = GetPlugin(container['type'])
                    plugin.send_file(self, path, query)
                    return

            ## Serve it from a "content" directory?
            base = os.path.join(SCRIPTDIR, *splitpath[:-1])
            path = os.path.join(base, 'content', splitpath[-1])

            if os.path.isfile(path):
                self.send_content_file(path)
                return

        ## Give up
        self.send_error(404)

    def authorize(self, tsn=None):
        # if allowed_clients is empty, we are completely open
        allowed_clients = config.getAllowedClients()
        if not allowed_clients or (tsn and config.isTsnInConfig(tsn)):
            return True
        client_ip = self.client_address[0]
        for allowedip in allowed_clients:
            if client_ip.startswith(allowedip):
                return True

        self.send_fixed('Unauthorized.', 'text/plain', 403)
        return False

    def log_message(self, format, *args):
        if 'NoLog' in args[0]:
            return

        self.server.logger.info("%s [%s] %s" % (self.address_string(),
                                self.log_date_time_string(), format%args))

    def send_fixed(self, page, mime, code=200, refresh=''):
        squeeze = (len(page) > 256 and mime.startswith('text') and
            'gzip' in self.headers.getheader('Accept-Encoding', ''))
        if squeeze:
            out = StringIO()
            gzip.GzipFile(mode='wb', fileobj=out).write(page)
            page = out.getvalue()
            out.close()
        self.send_response(code)
        self.send_header('Content-Type', mime)
        self.send_header('Content-Length', len(page))
        if squeeze:
            self.send_header('Content-Encoding', 'gzip')
        self.send_header('Expires', '0')
        if refresh:
            self.send_header('Refresh', refresh)
        self.send_header('Access-Control-Allow-Origin', '*') #uncomment for angular development in browser
        self.end_headers()
        self.wfile.write(page)
        self.wfile.flush()

    def send_xml(self, page):
        self.send_fixed(page, 'text/xml')

    def send_json(self, page):
        self.send_fixed(page, 'application/json; charset=utf-8')

    def send_html(self, page, code=200, refresh=''):
        self.send_fixed(page, 'text/html; charset=utf-8', code, refresh)

    def root_container(self):
        tsn = self.headers.getheader('TiVo_TCD_ID', '')
        tsnshares = config.getShares(tsn)
        tsncontainers = []
        for section, settings in tsnshares:
            try:
                mime = GetPlugin(settings['type']).CONTENT_TYPE
                if mime.split('/')[1] in ('tivo-videos', 'tivo-music',
                                          'tivo-photos'):
                    try:
                        settings['content_type'] = mime
                        tsncontainers.append((section, settings))
                    except Exception as msg:
                        self.server.logger.error(section + ' - ' + str(msg))
            except Exception as msg:
                self.server.logger.error(section + ' - ' + str(msg))
        t = Template(file=os.path.join(SCRIPTDIR, 'templates',
                                       'root_container.tmpl'),
                     filter=EncodeUnicode)
        if self.server.beacon.bd:
            t.renamed = self.server.beacon.bd.renamed
        else:
            t.renamed = {}
        t.containers = tsncontainers
        t.hostname = socket.gethostname()
        t.escape = escape
        t.quote = quote
        self.send_xml(str(t))

    def infopage(self):
        t = Template(file=os.path.join(SCRIPTDIR, 'templates',
                                       'info_page.tmpl'),
                     filter=EncodeUnicode)
        t.admin = ''

        if config.get_server('tivo_mak') and config.get_server('togo_path'):
            t.togo = '<br>Pull from TiVos:<br>'
        else:
            t.togo = ''

        for section, settings in config.getShares():
            plugin_type = settings.get('type')
            if plugin_type == 'settings':
                t.admin += ('<a href="/TiVoConnect?Command=Settings&amp;' +
                            'Container=' + quote(section) +
                            '">Settings</a><br>')
            elif plugin_type == 'togo' and t.togo:
                for tsn in config.tivos:
                    if tsn and 'address' in config.tivos[tsn]:
                        t.togo += ('<a href="/TiVoConnect?' +
                            'Command=NPL&amp;Container=' + quote(section) +  
                            '&amp;TiVo=' + config.tivos[tsn]['address'] +
                            '">' + config.tivos[tsn]['name'] +
                            '</a><br>')

        self.send_html(str(t))

    def unsupported(self, query):
        message = UNSUP % '\n'.join(['<li>%s: %s</li>' % (key, repr(value))
                                     for key, value in query.items()])
        text = BASE_HTML % message
        self.send_html(text, code=404)

    def redir(self, message, seconds=2):
        url = self.headers.getheader('Referer')
        if url:
            message += RELOAD % (url, seconds)
            refresh = '%d; url=%s' % (seconds, url)
        else:
            refresh = ''
        text = (BASE_HTML % message).encode('utf-8')
        self.send_html(text, refresh=refresh)
