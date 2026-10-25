import email.parser
import email.policy
import gzip
import http.server
import logging
import mimetypes
import os
import shutil
import socket
import socketserver
import sys
from email.utils import formatdate
from urllib.parse import parse_qs, quote, unquote_plus
from xml.sax.saxutils import escape

from Cheetah.Template import Template

import config
from plugin import EncodeUnicode, GetPlugin

# determine if application is a script file or frozen exe
SCRIPTDIR = os.path.dirname(os.path.abspath(__file__))
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


def parse_multipart(body, content_type):
    """Parse a multipart/form-data body into a parse_qs style dict."""
    message = email.parser.BytesParser(policy=email.policy.HTTP).parsebytes(
        b'Content-Type: ' + content_type.encode('latin-1') + b'\r\n\r\n' +
        body)
    query = {}
    for part in message.iter_parts():
        name = part.get_param('name', header='content-disposition')
        if not name:
            continue
        payload = part.get_payload(decode=True) or b''
        if part.get_filename() is None:
            payload = payload.decode(part.get_content_charset('utf-8'),
                                     'replace')
        query.setdefault(name, []).append(payload)
    return query


class TivoHTTPServer(socketserver.ThreadingMixIn, http.server.HTTPServer):
    daemon_threads = True

    def __init__(self, server_address, RequestHandlerClass):
        self.containers = {}
        self.stop = False
        self.restart = False
        self.in_service = False
        self.beacon = None
        self.logger = logging.getLogger('pyTivo')
        http.server.HTTPServer.__init__(self, server_address,
                                        RequestHandlerClass)

    def add_container(self, name, settings):
        if name in self.containers or name == 'TiVoConnect':
            raise Exception('Container Name in use')
        self.containers[name] = settings

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


class TivoHTTPHandler(http.server.BaseHTTPRequestHandler):
    wbufsize = 0x10000
    server_version = 'pyTivo/1.0'
    protocol_version = 'HTTP/1.1'
    sys_version = ''

    def setup(self):
        http.server.BaseHTTPRequestHandler.setup(self)
        # This allows pyTivo to die when user selects Stop Transfer on
        # the TiVo
        self.request.settimeout(180)

    def handle(self):
        # A TiVo routinely drops the connection mid-transfer (e.g. when
        # the user stops a download); that's not worth a traceback.
        try:
            http.server.BaseHTTPRequestHandler.handle(self)
        except (ConnectionError, socket.timeout) as msg:
            self.server.logger.info('%s: %s', self.address_string(), msg)

    def finish(self):
        # finish() flushes the buffered output, which fails the same way
        # if the TiVo has already hung up.
        try:
            http.server.BaseHTTPRequestHandler.finish(self)
        except (ConnectionError, socket.timeout):
            pass

    def address_string(self):
        host, port = self.client_address[:2]
        return host

    def version_string(self):
        """ Override version_string() so it doesn't include the Python
            version.

        """
        return self.server_version

    def get_tsn(self):
        return self.headers.get('TiVo_TCD_ID', self.headers.get('tsn', ''))

    def do_GET(self):
        tsn = self.get_tsn()
        if not self.authorize(tsn):
            return

        if tsn and (not config.tivos_found or tsn in config.tivos):
            attr = config.tivos.get(tsn, {})
            if 'address' not in attr:
                attr['address'] = self.address_string()
            if 'name' not in attr:
                if self.server.beacon:
                    attr['name'] = self.server.beacon.get_name(
                        attr['address'])
                else:
                    attr['name'] = attr['address']
            config.tivos[tsn] = attr

        if '?' in self.path:
            path, opts = self.path.split('?', 1)
            query = parse_qs(opts)
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
        tsn = self.get_tsn()
        if not self.authorize(tsn):
            return
        length = int(self.headers.get('Content-Length', 0))
        body = self.rfile.read(length)
        content_type = self.headers.get('Content-Type', '')
        if content_type.startswith('multipart/form-data'):
            query = parse_multipart(body, content_type)
        else:
            query = parse_qs(body.decode('utf-8', 'replace'),
                             keep_blank_values=True)
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
        if 'Command' in query and len(query['Command']) >= 1:

            command = query['Command'][0]

            # If we are looking at the root container
            if (command == 'QueryContainer' and
                ('Container' not in query or query['Container'][0] == '/')):
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
                if splitpath and '..' not in splitpath:
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
                    return

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
        try:
            lmdate = os.path.getmtime(path)
            handle = open(path, 'rb')
        except OSError:
            self.send_error(404)
            return

        with handle:
            # Send the header
            mime = mimetypes.guess_type(path)[0]
            self.send_response(200)
            if mime:
                self.send_header('Content-Type', mime)
            self.send_header('Content-Length', os.path.getsize(path))
            self.send_header('Last-Modified', formatdate(lmdate, usegmt=True))
            self.end_headers()

            # Send the body of the file
            try:
                shutil.copyfileobj(handle, self.wfile)
            except OSError:
                pass
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
        if args and isinstance(args[0], str) and 'NoLog' in args[0]:
            return

        self.server.logger.info("%s [%s] %s" % (self.address_string(),
                                self.log_date_time_string(), format % args))

    def send_fixed(self, page, mime, code=200, refresh=''):
        if isinstance(page, str):
            page = page.encode('utf-8')
        squeeze = (len(page) > 256 and mime.startswith('text') and
            'gzip' in self.headers.get('Accept-Encoding', ''))
        if squeeze:
            page = gzip.compress(page)
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
        tsn = self.headers.get('TiVo_TCD_ID', '')
        tsnshares = config.getShares(tsn)
        tsncontainers = []
        for section, settings in tsnshares:
            try:
                mime = GetPlugin(settings['type']).CONTENT_TYPE
                if mime.split('/')[1] in ('tivo-videos', 'tivo-music',
                                          'tivo-photos'):
                    settings['content_type'] = mime
                    tsncontainers.append((section, settings))
            except Exception as msg:
                self.server.logger.error(section + ' - ' + str(msg))
        t = Template(file=os.path.join(SCRIPTDIR, 'templates',
                                       'root_container.tmpl'),
                     filter=EncodeUnicode)
        if self.server.beacon and self.server.beacon.bd:
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
                            '">' + escape(config.tivos[tsn].get('name',
                                          config.tivos[tsn]['address'])) +
                            '</a><br>')

        self.send_html(str(t))

    def unsupported(self, query):
        message = UNSUP % '\n'.join(['<li>%s: %s</li>' %
                                     (escape(key), escape(repr(value)))
                                     for key, value in query.items()])
        text = BASE_HTML % message
        self.send_html(text, code=404)

    def redir(self, message, seconds=2):
        url = self.headers.get('Referer')
        if url:
            message += RELOAD % (escape(url), seconds)
            refresh = '%d; url=%s' % (seconds, url)
        else:
            refresh = ''
        text = BASE_HTML % message
        self.send_html(text, refresh=refresh)
