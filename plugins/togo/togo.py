import html
import http.cookiejar
import json
import logging
import os
import ssl
import struct
import subprocess
import sys
import threading
import time
import urllib.error
import urllib.parse
import urllib.request
import warnings
from datetime import datetime, timezone
from urllib.parse import quote, unquote
from xml.dom import minidom

from Cheetah.Template import Template

import config
import metadata
from plugin import EncodeUnicode, Plugin

try:
    from videoredo import interface
except ImportError:     # VideoReDo needs Windows and comtypes
    interface = None

logger = logging.getLogger('pyTivo.togo')
tag_data = metadata.tag_data

# determine if application is a script file or frozen exe
SCRIPTDIR = os.path.dirname(__file__)
if getattr(sys, 'frozen', False):
    SCRIPTDIR = os.path.join(sys._MEIPASS, 'plugins', 'togo')

CLASS_NAME = 'ToGo'

# Characters to remove from filenames

BADCHAR = {'\\': '-', '/': '-', ':': ' -', ';': ',', '*': '.',
           '?': '.', '!': '.', '"': "'", '<': '(', '>': ')', '|': ' '}

# Default top-level share path

DEFPATH = '/TiVoConnect?Command=QueryContainer&Container=/NowPlaying'

# Some error/status message templates

MISSING = """<h3>Missing Data</h3> <p>You must set both "tivo_mak" and
"togo_path" before using this function.</p>"""

TRANS_QUEUE = """<h3>Queued for Transfer</h3> <p>%s</p> <p>queued for
transfer to:</p> <p>%s</p>"""

TRANS_STOP = """<h3>Transfer Stopped</h3> <p>Your transfer of:</p>
<p>%s</p> <p>has been stopped.</p>"""

UNQUEUE = """<h3>Removed from Queue</h3> <p>%s</p> <p>has been removed
from the queue.</p>"""

UNABLE = """<h3>Unable to Connect to TiVo</h3> <p>pyTivo was unable to
connect to the TiVo at %s.</p> <p>This is most likely caused by an
incorrect Media Access Key. Please return to the Settings page and
double check your <b>tivo_mak</b> setting.</p> <pre>%s</pre>"""

# Preload the templates
tnname = os.path.join(SCRIPTDIR, 'templates', 'npl.tmpl')
with open(tnname, encoding='utf-8') as f:
    NPL_TEMPLATE = f.read()

mswindows = (sys.platform == "win32")

status = {} # Global variable to control download threads
tivo_cache = {} # Cache of TiVo NPL
json_cache = {} # Cache of TiVo json NPL data
queue = {} # Recordings to download -- list per TiVo
basic_meta = {} # Data from NPL, parsed, indexed by progam URL
details_urls = {} # URLs for extended data, indexed by main URL

def null_cookie(name, value):
    return http.cookiejar.Cookie(0, name, value, None, False, '', False,
        False, '', False, False, None, False, None, None, None)

def tivo_ssl_context():
    """TLS settings for talking to a TiVo on the local network.

    TiVos use self-signed certificates, and older models only speak TLS
    configurations that current OpenSSL rejects by default. Access is
    still protected by the Media Access Key (HTTP digest auth).
    """
    ctx = ssl.create_default_context()
    ctx.check_hostname = False
    ctx.verify_mode = ssl.CERT_NONE
    try:
        with warnings.catch_warnings():
            warnings.simplefilter('ignore', DeprecationWarning)
            ctx.minimum_version = ssl.TLSVersion.TLSv1
        ctx.set_ciphers('DEFAULT:@SECLEVEL=0')
    except (ValueError, ssl.SSLError):
        pass
    ctx.options |= getattr(ssl, 'OP_LEGACY_SERVER_CONNECT', 0x4)
    return ctx

auth_handler = urllib.request.HTTPPasswordMgrWithDefaultRealm()
cj = http.cookiejar.CookieJar()
cj.set_cookie(null_cookie('sid', 'ADEADDA7EDEBAC1E'))
tivo_opener = urllib.request.build_opener(
    urllib.request.HTTPSHandler(context=tivo_ssl_context()),
    urllib.request.HTTPCookieProcessor(cj),
    urllib.request.HTTPBasicAuthHandler(auth_handler),
    urllib.request.HTTPDigestAuthHandler(auth_handler))

tsn = config.get_server('togo_tsn')
if tsn:
    tivo_opener.addheaders.append(('TSN', tsn))

if mswindows:
    def PreventComputerFromSleeping(prevent=True):
        import ctypes

        ES_CONTINUOUS = 0x80000000
        ES_SYSTEM_REQUIRED = 0x00000001
        # SetThreadExecutionState returns 0 when failed, which is ignored. The function should be supported from windows XP and up.
        if prevent:
            logger.info('PC sleep has been disabled')
            ctypes.windll.kernel32.SetThreadExecutionState(ES_CONTINUOUS | ES_SYSTEM_REQUIRED)
        else:
            logger.info('PC sleep has been enabled')
            ctypes.windll.kernel32.SetThreadExecutionState(ES_CONTINUOUS)

else:
    def PreventComputerFromSleeping(prevent=True):
        # No preventComputerFromSleeping for MacOS and Linux yet.
        pass

def tivo_base_url(tivoIP, attrs):
    protocol = attrs.get('protocol', 'https')
    ip_port = '%s:%d' % (tivoIP, int(attrs.get('port', 443)))
    path = attrs.get('path', DEFPATH)
    return ip_port, '%s://%s%s' % (protocol, ip_port, path)

class ToGo(Plugin):
    CONTENT_TYPE = 'text/html'

    def tivo_open(self, url):
        # Loop just in case we get a server busy message
        while True:
            try:
                # Open the URL using our authentication/cookie opener
                return tivo_opener.open(url, timeout=60)

            # Do a retry if the TiVo responds that the server is busy
            except urllib.error.HTTPError as e:
                if e.code == 503:
                    time.sleep(5)
                    continue

                # Log and throw the error otherwise
                logger.error(e)
                raise

    def GetTiVoList(self, handler, query):
        json_config = {}
        for tsn in config.tivos:
            if tsn and 'address' in config.tivos[tsn]:
                json_config[tsn] = {}
                if 'name' in config.tivos[tsn]:
                    json_config[tsn]['name'] = config.tivos[tsn]['name']
                else:
                    json_config[tsn]['name'] = "Unnamed TiVo: " + tsn

                json_config[tsn]['tsn'] = tsn
                json_config[tsn]['address'] = config.tivos[tsn]['address']
                if 'port' in config.tivos[tsn]:
                    json_config[tsn]['port'] = config.tivos[tsn]['port']
                else:
                    json_config[tsn]['port'] = 443
            else:
                if tsn:
                    logger.info("No address for TSN: " + tsn)
                else:
                    logger.error("Invalid TiVo detected")

        handler.send_json(json.dumps(json_config))

    def GetShowsList(self, handler, query):
        json_config = {}
        if 'TiVo' in query:
            tivoIP = query['TiVo'][0]
            tsn = config.tivos_by_ip(tivoIP)
            attrs = config.tivos.get(tsn, {})
            tivo_name = attrs.get('name', tivoIP)
            tivo_mak = config.get_tsn('tivo_mak', tsn)

            ip_port, baseurl = tivo_base_url(tivoIP, attrs)

            # Get the total item count first
            theurl = baseurl + '&Recurse=Yes&ItemCount=0'
            auth_handler.add_password('TiVo DVR', ip_port, 'tivo', tivo_mak)
            try:
                page = self.tivo_open(theurl)
            except OSError as e:
                logger.error("Unable to open TiVo")
                logger.info("Check your Media Access Key")
                handler.send_error(404)
                return

            try:
                xmldoc = minidom.parse(page)
                page.close()
            except Exception:
                logger.error("XML parser error")
                handler.send_error(404)
                return

            LastChangeDate = tag_data(xmldoc, 'TiVoContainer/Details/LastChangeDate')

            # Check date of cache
            if (tsn in json_cache and json_cache[tsn]['lastChangeDate'] == LastChangeDate):
                logger.info("Shows retrieved from cache")
                handler.send_json(json_cache[tsn]['data'])
                return

            # loop through grabbing 50 items at a time (50 is max TiVo will return)
            try:
                TotalItems = int(tag_data(xmldoc, 'TiVoContainer/Details/TotalItems'))
            except ValueError:
                TotalItems = 0

            if TotalItems <= 0:
                handler.send_json(json.dumps(json_config))
                return

            GotItems = 0
            GeneratedID = 0
            while (GotItems < TotalItems):
                logger.debug("Retrieving shows " + str(GotItems) + "-" + str(GotItems+50) + " of " + str(TotalItems) + " from " + tivo_name )
                theurl = baseurl + '&Recurse=Yes&ItemCount=50'
                theurl += '&AnchorOffset=%d' % GotItems
                auth_handler.add_password('TiVo DVR', ip_port, 'tivo', tivo_mak)
                try:
                    page = self.tivo_open(theurl)
                except OSError as e:
                    handler.send_error(404)
                    return

                try:
                    xmldoc = minidom.parse(page)
                    items = xmldoc.getElementsByTagName('Item')
                    page.close()
                except Exception:
                    logger.error("XML parser error")
                    break

                if len(items) <= 0:
                    logger.info("items collection empty")
                    break

                for item in items:
                    SeriesID = tag_data(item, 'Details/SeriesId')
                    if (not SeriesID):
                        SeriesID = 'PS%08d' % GeneratedID
                        GeneratedID += 1

                    if (not SeriesID in json_config):
                        json_config[SeriesID] = {}

                    EpisodeID = tag_data(item, 'Details/ProgramId')
                    if (not EpisodeID):
                        EpisodeID = 'PE%08d' % GeneratedID
                        GeneratedID += 1

                    # Check for duplicate episode IDs and replace with generated ID
                    while EpisodeID in json_config[SeriesID]:
                        EpisodeID = 'PE%08d' % GeneratedID
                        GeneratedID += 1

                    json_config[SeriesID][EpisodeID] = {}

                    json_config[SeriesID][EpisodeID]['title'] = tag_data(item, 'Details/Title')
                    json_config[SeriesID][EpisodeID]['url'] = tag_data(item, 'Links/Content/Url')
                    json_config[SeriesID][EpisodeID]['detailsUrl'] = tag_data(item, 'Links/TiVoVideoDetails/Url')
                    json_config[SeriesID][EpisodeID]['episodeTitle'] = tag_data(item, 'Details/EpisodeTitle')
                    json_config[SeriesID][EpisodeID]['description'] = tag_data(item, 'Details/Description')
                    json_config[SeriesID][EpisodeID]['recordDate'] = tag_data(item, 'Details/CaptureDate')
                    json_config[SeriesID][EpisodeID]['duration'] = tag_data(item, 'Details/Duration')
                    try:
                        json_config[SeriesID][EpisodeID]['sourceSize'] = int(tag_data(item, 'Details/SourceSize'))
                    except ValueError:
                        json_config[SeriesID][EpisodeID]['sourceSize'] = 0
                    json_config[SeriesID][EpisodeID]['channel'] = tag_data(item, 'Details/SourceChannel')
                    json_config[SeriesID][EpisodeID]['stationID'] = tag_data(item, 'Details/SourceStation')
                    json_config[SeriesID][EpisodeID]['episodeID'] = EpisodeID
                    json_config[SeriesID][EpisodeID]['seriesID'] = SeriesID

                    if tag_data(item, 'Details/InProgress') == 'Yes':
                        json_config[SeriesID][EpisodeID]['inProgress'] = True
                    else:
                        json_config[SeriesID][EpisodeID]['inProgress'] = False

                    if tag_data(item, 'Details/CopyProtected') == 'Yes':
                        json_config[SeriesID][EpisodeID]['isProtected'] = True
                    else:
                        json_config[SeriesID][EpisodeID]['isProtected'] = False

                    if tag_data(item, 'Links/CustomIcon/Url') == 'urn:tivo:image:suggestion-recording':
                        json_config[SeriesID][EpisodeID]['isSuggestion'] = True
                    else:
                        json_config[SeriesID][EpisodeID]['isSuggestion'] = False

                    if tag_data(item, 'Details/CopyProtected') == 'Yes':
                        json_config[SeriesID][EpisodeID]['icon'] = 'protected'
                    elif tag_data(item, 'Links/CustomIcon/Url') == 'urn:tivo:image:expires-soon-recording':
                        json_config[SeriesID][EpisodeID]['icon'] = 'expiring'
                    elif tag_data(item, 'Links/CustomIcon/Url') == 'urn:tivo:image:expired-recording':
                        json_config[SeriesID][EpisodeID]['icon'] = 'expired'
                    elif tag_data(item, 'Links/CustomIcon/Url') == 'urn:tivo:image:save-until-i-delete-recording':
                        json_config[SeriesID][EpisodeID]['icon'] = 'kuid'
                    elif tag_data(item, 'Links/CustomIcon/Url') == 'urn:tivo:image:suggestion-recording':
                        json_config[SeriesID][EpisodeID]['icon'] = 'suggestion'
                    elif tag_data(item, 'Links/CustomIcon/Url') == 'urn:tivo:image:in-progress-recording':
                        json_config[SeriesID][EpisodeID]['icon'] = 'inprogress'
                    else:
                        json_config[SeriesID][EpisodeID]['icon'] = 'normal'

                    url = urllib.parse.urljoin(baseurl, json_config[SeriesID][EpisodeID]['url'])
                    json_config[SeriesID][EpisodeID]['url'] = url
                    if not url in basic_meta:
                        basic_meta[url] = metadata.from_container(item)
                        if 'detailsUrl' in json_config[SeriesID][EpisodeID]:
                            details_urls[url] = json_config[SeriesID][EpisodeID]['detailsUrl']

                itemCount = tag_data(xmldoc, 'TiVoContainer/ItemCount')
                try:
                    logger.debug("Retrieved " + itemCount + " from " + tivo_name)
                    GotItems += int(itemCount)
                except ValueError:
                    GotItems += len(items)


            # Cache data for reuse
            json_cache[tsn] = {}
            json_cache[tsn]['data'] = json.dumps(json_config)
            json_cache[tsn]['lastChangeDate'] = LastChangeDate

            handler.send_json(json_cache[tsn]['data'])
        else:
            handler.send_json(json.dumps(json_config))

    def GetQueueList(self, handler, query):
        json_config = {}
        if 'TiVo' in query:
            tivoIP = query['TiVo'][0]
            if tivoIP in queue:
                json_config['urls'] = []
                for url in queue[tivoIP]:
                    json_config['urls'].append(url)

        handler.send_json(json.dumps(json_config))

    def GetTotalQueueCount(self, handler, query):
        json_config = {}
        json_config['count'] = 0

        for tivoIP in queue:
            json_config['count'] += len(queue[tivoIP])

        handler.send_json(json.dumps(json_config))

    def GetStatus(self, handler, query):
        json_config = {}

        if 'Url' in query:
            url = query['Url'][0]
            if url in status:
                state = 'queued'
                if status[url]['running'] == True:
                    state = 'running'
                elif status[url]['postprocessing'] == True:
                    state = 'postprocessing'
                elif status[url]['finished'] == True:
                    if status[url]['error'] == '':
                        state = 'finished'
                    else:
                        state = 'error'
                        json_config['error'] = status[url]['error']

                json_config['state'] = state
                json_config['rate'] = status[url]['rate']
                json_config['percent'] = status[url]['percent']
                json_config['size'] = status[url]['size']
                json_config['status'] = status[url]['status']
                json_config['retry'] = status[url]['retry']
                json_config['maxRetries'] = status[url]['ts_max_retries']
                json_config['errorCount'] = status[url]['ts_error_count']

        handler.send_json(json.dumps(json_config))

    def NPL(self, handler, query):

        def getint(thing):
            try:
                result = int(thing)
            except (TypeError, ValueError):
                result = 0
            return result


        shows_per_page = 50 # Change this to alter the number of shows returned (max is 50)
        if 'ItemCount' in query:
            shows_per_page = int(query['ItemCount'][0])

        if (shows_per_page > 50):
            shows_per_page = 50

        folder = ''
        FirstAnchor = ''
        has_tivodecode = bool(config.get_bin('tivodecode'))
        has_tivolibre = bool(config.get_bin('tivolibre'))

        if 'TiVo' in query:
            tivoIP = query['TiVo'][0]
            tsn = config.tivos_by_ip(tivoIP)
            attrs = config.tivos.get(tsn, {})
            tivo_name = attrs.get('name', tivoIP)
            tivo_mak = config.get_tsn('tivo_mak', tsn)

            ip_port, baseurl = tivo_base_url(tivoIP, attrs)
            theurl = baseurl
            if 'Folder' in query:
                folder = query['Folder'][0]
                theurl = urllib.parse.urljoin(theurl, folder)
            theurl += '&ItemCount=%d' % shows_per_page
            if 'AnchorItem' in query:
                theurl += '&AnchorItem=' + quote(query['AnchorItem'][0])
            if 'AnchorOffset' in query:
                theurl += '&AnchorOffset=' + query['AnchorOffset'][0]
            if 'SortOrder' in query:
                theurl += '&SortOrder=' + query['SortOrder'][0]
            if 'Recurse' in query:
                    theurl += '&Recurse=' + query['Recurse'][0]

            if (theurl not in tivo_cache or
                (time.time() - tivo_cache[theurl]['thepage_time']) >= 60):
                # if page is not cached or old then retreive it
                auth_handler.add_password('TiVo DVR', ip_port, 'tivo', tivo_mak)
                try:
                    page = self.tivo_open(theurl)
                except OSError as e:
                    handler.redir(UNABLE % (html.escape(tivoIP),
                                            html.escape(str(e))), 10)
                    return
                tivo_cache[theurl] = {'thepage': minidom.parse(page),
                                      'thepage_time': time.time()}
                page.close()

            xmldoc = tivo_cache[theurl]['thepage']
            items = xmldoc.getElementsByTagName('Item')

            TotalItems = tag_data(xmldoc, 'TiVoContainer/Details/TotalItems')
            ItemStart = tag_data(xmldoc, 'TiVoContainer/ItemStart')
            ItemCount = tag_data(xmldoc, 'TiVoContainer/ItemCount')
            title = tag_data(xmldoc, 'TiVoContainer/Details/Title')
            if items:
                FirstAnchor = tag_data(items[0], 'Links/Content/Url')

            data = []
            for item in items:
                entry = {}
                for tag in ('CopyProtected', 'ContentType'):
                    value = tag_data(item, 'Details/' + tag)
                    if value:
                        entry[tag] = value
                if entry.get('ContentType', '').startswith('x-tivo-container'):
                    entry['Url'] = tag_data(item, 'Links/Content/Url')
                    entry['Title'] = tag_data(item, 'Details/Title')
                    entry['TotalItems'] = tag_data(item, 'Details/TotalItems')
                    lc = tag_data(item, 'Details/LastCaptureDate')
                    if not lc:
                        lc = tag_data(item, 'Details/LastChangeDate')
                    entry['LastChangeDate'] = time.strftime('%b %d, %Y',
                        time.localtime(int(lc, 16)))
                else:
                    keys = {'Icon': 'Links/CustomIcon/Url',
                            'Url': 'Links/Content/Url',
                            'Details': 'Links/TiVoVideoDetails/Url',
                            'SourceSize': 'Details/SourceSize',
                            'Duration': 'Details/Duration',
                            'CaptureDate': 'Details/CaptureDate'}
                    for key in keys:
                        value = tag_data(item, keys[key])
                        if value:
                            entry[key] = value

                    if 'SourceSize' in entry:
                        rawsize = entry['SourceSize']
                        entry['SourceSize'] = metadata.human_size(rawsize)

                    if 'Duration' in entry:
                        dur = getint(entry['Duration']) // 1000
                        entry['Duration'] = ( '%d:%02d:%02d' %
                            (dur // 3600, (dur % 3600) // 60, dur % 60) )

                    if 'CaptureDate' in entry:
                        entry['CaptureDate'] = time.strftime('%b %d, %Y',
                            time.localtime(int(entry['CaptureDate'], 16)))

                    url = urllib.parse.urljoin(baseurl, entry['Url'])
                    entry['Url'] = url
                    if url in basic_meta:
                        entry.update(basic_meta[url])
                    else:
                        basic_data = metadata.from_container(item)
                        entry.update(basic_data)
                        basic_meta[url] = basic_data
                        if 'Details' in entry:
                            details_urls[url] = entry['Details']

                data.append(entry)
        else:
            data = []
            tivoIP = ''
            TotalItems = 0
            ItemStart = 0
            ItemCount = 0
            title = ''
            tsn = ''
            tivo_name = ''


        t = Template(NPL_TEMPLATE, filter=EncodeUnicode)
        t.quote = quote
        t.folder = folder
        t.status = status
        if tivoIP in queue:
            t.queue = queue[tivoIP]
        t.has_tivodecode = has_tivodecode
        t.has_tivolibre = has_tivolibre
        t.togo_mpegts = config.is_ts_capable(tsn)
        t.tname = tivo_name
        t.tivoIP = tivoIP
        t.container = handler.cname
        t.data = data
        t.len = len
        t.TotalItems = getint(TotalItems)
        t.ItemStart = getint(ItemStart)
        t.ItemCount = getint(ItemCount)
        t.FirstAnchor = quote(FirstAnchor)
        t.shows_per_page = shows_per_page
        t.title = title
        handler.send_html(str(t), refresh='300')


    def get_out_file(self, url, tivoIP, togo_path):
        # Use TiVo Desktop style naming
        if url in basic_meta:
            if 'title' in basic_meta[url]:
                title = basic_meta[url]['title']

                episodeTitle = ''
                if 'episodeTitle' in basic_meta[url]:
                    episodeTitle = basic_meta[url]['episodeTitle']

                recordDate = datetime.now()
                if 'recordDate' in basic_meta[url]:
                    recordDate = datetime.fromtimestamp(int(basic_meta[url]['recordDate'], 0), timezone.utc)

                callSign = ''
                if 'callsign' in basic_meta[url]:
                    callSign = basic_meta[url]['callsign']

                count = 1
                while True:
                    fileName = title

                    sortable = config.get_server_bool('togo_sortable_names')

                    if sortable == True:
                        fileName += ' - '
                        fileName += recordDate.strftime('%Y-%m-%d')

                        if len(episodeTitle):
                            fileName += ' - \'' + episodeTitle + '\''

                        if len(callSign):
                            fileName += ' (' + callSign + ')'
                    else:
                        if len(episodeTitle):
                            fileName += ' - \'' + episodeTitle + '\''

                        fileName += ' (Recorded '
                        fileName += recordDate.strftime('%b %d, %Y')
                        if len(callSign):
                            fileName += ', ' + callSign
                        fileName += ')'

                    ts = status[url]['ts_format'] and config.is_ts_capable(config.tivos_by_ip(tivoIP))
                    if not status[url]['decode']:
                        if ts:
                            fileName += ' (TS)'
                        else:
                            fileName += ' (PS)'

                    if count > 1:
                        fileName += ' (%d)' % count

                    if status[url]['decode']:
                        if ts:
                            fileName += '.ts'
                        else:
                            fileName += '.mpg'
                    else:
                        fileName += '.tivo'

                    for ch in BADCHAR:
                        fileName = fileName.replace(ch, BADCHAR[ch])

                    if os.path.isfile(os.path.join(togo_path, fileName)):
                        count += 1
                        continue

                    return os.path.join(togo_path, fileName)

        # If we get here then use old style naming
        parse_url = urllib.parse.urlparse(url)

        name = unquote(parse_url[2]).split('/')[-1].split('.')
        if len(name) < 2:
            name.append('tivo')
        query_string = unquote(parse_url[4])
        if 'id=' in query_string:
            name.insert(-1, ' - ' + query_string.split('id=')[1])
        ts = status[url]['ts_format'] and config.is_ts_capable(config.tivos_by_ip(tivoIP))
        if status[url]['decode']:
            if ts:
                name[-1] = 'ts'
            else:
                name[-1] = 'mpg'
        else:
            if ts:
                name.insert(-1, ' (TS)')
            else:
                name.insert(-1, ' (PS)')

        base, ext = ''.join(name[:-1]), name[-1]
        for ch in BADCHAR:
            base = base.replace(ch, BADCHAR[ch])

        fileName = '%s.%s' % (base, ext)
        count = 2
        while os.path.isfile(os.path.join(togo_path, fileName)):
            fileName = '%s (%d).%s' % (base, count, ext)
            count += 1

        return os.path.join(togo_path, fileName)


    def get_tivo_file(self, tivoIP, url, mak, togo_path):
        # global status
        status[url].update({'running': True, 'queued': False})

        outfile = self.get_out_file(url, tivoIP, togo_path)

        auth_handler.add_password('TiVo DVR', url, 'tivo', mak)
        try:
            ts_format = False
            if status[url]['ts_format'] and config.is_ts_capable(config.tivos_by_ip(tivoIP)):
                ts_format = True
                handle = self.tivo_open(url + '&Format=video/x-tivo-mpeg-ts')
            else:
                handle = self.tivo_open(url)
        except Exception as msg:
            status[url]['running'] = False
            status[url]['finished'] = True
            status[url]['error'] = str(msg)
            return

        tivo_name = config.tivos.get(config.tivos_by_ip(tivoIP), {}).get('name', tivoIP)

        logger.info('[%s] Start getting "%s" from %s' %
                    (time.strftime('%d/%b/%Y %H:%M:%S'), outfile, tivo_name))

        has_tivodecode = bool(config.get_bin('tivodecode'))
        has_tivolibre = bool(config.get_bin('tivolibre'))
        decode = status[url]['decode'] and (has_tivodecode or has_tivolibre)
        if decode:
            fname = outfile

            decoder_path = config.get_bin('tivodecode')
            tcmd = [decoder_path, '-m', mak, '-o', fname, '-']
            if has_tivolibre:
                decoder_path = config.get_bin('tivolibre')
                tcmd = [decoder_path, '-m', mak, '-o', fname]

            tivodecode = subprocess.Popen(tcmd, stdin=subprocess.PIPE,
                                          stderr=subprocess.PIPE,
                                          bufsize=(512 * 1024))
            f = tivodecode.stdin
            # Drain stderr as we go so a chatty decoder can't block
            decoder_errors = []
            stderr_reader = threading.Thread(
                target=lambda: decoder_errors.append(tivodecode.stderr.read()),
                daemon=True)
            stderr_reader.start()
        else:
            f = open(outfile, 'wb')


        save_txt = status[url]['save']

        length = 0
        bytes_written = 0
        start_time = time.time()
        last_interval = start_time
        now = start_time
        retry_download = False
        sync_loss = False
        ts_error_mode = config.get_server('togo_ts_error_mode', 'ignore')
        slow_on_retry = config.get_server_bool('togo_slow_on_retry', True)
        try:
            download_speed = int(config.get_server('togo_download_speed', 5))
        except ValueError:
            download_speed = 5
        download_delay = max(5 - download_speed, 0) * 0.01
        download_delay += status[url]['download_delay']
        logger.info('Download delay set to %f' % download_delay)

        try:
            # Download just the header first so remaining bytes are packet aligned for TS
            tivo_header = handle.read(16)
            length += len(tivo_header)
            f.write(tivo_header)

            tivo_header_size = struct.unpack_from('>L', tivo_header, 10)[0]
            output = handle.read(tivo_header_size-16)
            length += len(output)
            f.write(output)

            bytes_written += length
            while status[url]['running']:
                output = handle.read(282000) # Size needs to be divisible by 188 (524144)
                if not output:
                    break

                if ts_format:
                    cur_byte = 0
                    sync_loss_start = -1
                    while cur_byte < len(output):
                        if struct.unpack_from('>B', output, cur_byte)[0] != 0x47:
                            sync_loss = True
                            status[url]['ts_error_count'] += 1

                            if sync_loss_start == -1:
                                sync_loss_start = cur_byte
                        else:
                            if sync_loss_start != -1:
                                logger.info('TS sync loss detected: %d - %d' % ((sync_loss_start + bytes_written), (cur_byte + bytes_written)))
                                sync_loss_start = -1

                        cur_byte += 188

                    if sync_loss and ts_error_mode !=  'ignore':
                        if status[url]['retry'] < status[url]['ts_max_retries']:
                            retry_download = True

                        if ts_error_mode ==  'best':
                            if status[url]['best_error_count'] > 0:
                                if status[url]['ts_error_count'] >= status[url]['best_error_count']:
                                    status[url]['running'] = False
                                    status[url]['error'] = 'Transport stream error detected'
                                    break

                        elif ts_error_mode ==  'reject':
                            status[url]['running'] = False
                            status[url]['error'] = 'Transport stream error detected'
                            break

                length += len(output)
                bytes_written += len(output)
                f.write(output)
                now = time.time()
                elapsed = now - last_interval
                if elapsed >= 1:
                    status[url]['rate'] = (length * 8.0) / elapsed
                    status[url]['size'] += length
                    length = 0
                    last_interval = now

                time.sleep(download_delay)

            if status[url]['running']:
                if not sync_loss:
                    status[url]['error'] = ''

        except Exception as msg:
            status[url]['error'] = 'Error downloading file'
            status[url]['running'] = False
            logger.info(msg)

        handle.close()
        try:
            f.close()
        except OSError as msg:
            logger.info(msg)

        if decode:
            tivodecode.wait()
            stderr_reader.join(5)
            decoder_errors = b''.join(decoder_errors).decode('utf-8', 'replace')
            if tivodecode.returncode:
                # e.g. tivolibre.exe reports "Java Runtime Environment not
                # found" when there is no JRE next to it or on the system
                message = decoder_errors.strip().replace(mak, '<MAK>')
                status[url]['error'] = ('%s failed (exit code %d): %s' % (
                    os.path.basename(decoder_path), tivodecode.returncode,
                    message or 'no error output'))
                status[url]['running'] = False
                logger.error(status[url]['error'])

        status[url]['size'] += length
        if status[url]['running']:
            mega_elapsed = (now - start_time) * 1024 * 1024
            if mega_elapsed < 1:
                mega_elapsed = 1
            size = status[url]['size']
            rate = size * 8.0 / mega_elapsed
            logger.info('[%s] Done getting "%s" from %s, %d bytes, %.2f Mb/s' %
                        (time.strftime('%d/%b/%Y %H:%M:%S'), outfile,
                         tivo_name, size, rate))

            status[url]['running'] = False

            if ts_error_mode == 'best' and os.path.isfile(status[url]['best_file']):
                try:
                    os.remove(status[url]['best_file'])
                    if os.path.isfile(status[url]['best_file'] + '.txt'):
                        os.remove(status[url]['best_file'] + '.txt')
                except OSError:
                    logger.error('Unable to delete previous best file: %s' % status[url]['best_file'])

            if sync_loss:
                outfile_name = os.path.basename(outfile).split('.')
                count = 1
                while True:
                    outfile_name.insert(-1, ' (^' + str(status[url]['ts_error_count']) + '_' + str(status[url]['retry']) + ')')
                    if count > 1:
                        outfile_name.insert(-1, ' (' + str(count) + ')')

                    outfile_name.insert(-1, '.')
                    new_outfile = os.path.join(togo_path, ''.join(outfile_name))
                    if os.path.isfile(new_outfile):
                        count += 1
                        continue

                    try:
                        os.rename(outfile, new_outfile)
                        outfile = new_outfile
                    except OSError:
                        logger.error('Error renaming file for TS sync loss')
                        retry_download = False

                    break

            metafile_name = ''
            if save_txt and os.path.isfile(outfile):
                meta = dict(basic_meta.get(url, {}))
                try:
                    handle = self.tivo_open(details_urls[url])
                    meta.update(metadata.from_details(handle.read()))
                    handle.close()
                except Exception as msg:
                    logger.info('Unable to get details for %s: %s', url, msg)

                metafile_name = outfile + '.txt'
                with open(metafile_name, 'w', encoding='utf-8') as metafile:
                    metadata.dump(metafile, meta)

            status[url]['best_file'] = outfile
            status[url]['best_error_count'] = status[url]['ts_error_count']

            if retry_download:
                status[url]['rate'] = 0
                status[url]['size'] = 0
                status[url]['queued'] = True
                status[url]['retry'] += 1
                status[url]['ts_error_count'] = 0
                logger.info('TS sync losses detected, retrying download (%d)' % status[url]['retry'])

                if slow_on_retry:
                    status[url]['download_delay'] += 0.01
                    logger.info('Increasing download delay')

                queue[tivoIP].insert(1, url)
            else:
                if status[url]['postprocess'] != 'none':
                    self.post_process_file(url, outfile, metafile_name)
                else:
                    status[url]['finished'] = True


        else:
            try:
                os.remove(outfile)
            except OSError:
                pass
            logger.info('[%s] Transfer of "%s" from %s aborted' %
                        (time.strftime('%d/%b/%Y %H:%M:%S'), outfile,
                         tivo_name))

            if retry_download:
                status[url]['rate'] = 0
                status[url]['size'] = 0
                status[url]['queued'] = True
                status[url]['retry'] += 1
                status[url]['ts_error_count'] = 0
                logger.info('TS sync losses detected, retrying download (%d)' % status[url]['retry'])

                if slow_on_retry:
                    status[url]['download_delay'] += 0.01
                    logger.info('Increasing download delay')

                queue[tivoIP].insert(1, url)
            else:
                if status[url]['best_file'] and status[url]['postprocess'] != 'none':
                    self.post_process_file(url, status[url]['best_file'])
                else:
                    status[url]['finished'] = True


    def post_process_file(self, url, outfile, metafile=''):
        vrd_post_processing = status[url]['postprocess']
        if not vrd_post_processing or vrd_post_processing == 'none':
            status[url]['finished'] = True
        else:
            out_folder = config.get_server('vrd_output_folder', '')
            if not out_folder:
                out_folder = os.path.dirname(outfile)

            vrd = interface.VideoReDo() if interface else None
            if vrd is not None and vrd.vrd is not None:
                if vrd_post_processing == 'adscan':
                    profile = status[url]['postprocess_profile']
                    vrd.ad_scan(outfile, profile)
                    status[url]['postprocessing'] = True
                elif vrd_post_processing == 'qsf':
                    decrypt_qsf = status[url]['postprocess_decrypt']
                    vrd.quick_stream_fix(outfile, decrypt_qsf, out_folder)
                    status[url]['postprocessing'] = True
                elif vrd_post_processing == 'profile':
                    profile = status[url]['postprocess_profile']
                    vrd.save_to_profile(outfile, profile, out_folder)
                    status[url]['postprocessing'] = True
                else:
                    status[url]['finished'] = True

                if status[url]['postprocessing'] == True:
                    time.sleep(2)

                    vrd_status = vrd.get_status()
                    while vrd_status['state'] != 'none':
                        if vrd_status['state'] == 'paused':
                            vrd.pause_output(False)  # should never be paused, but just in case

                        status[url]['percent'] = vrd_status['percent']
                        status[url]['status'] = vrd_status['text']
                        time.sleep(2)
                        vrd_status = vrd.get_status()

                    vrd.close_file()
                    time.sleep(2)

                    vrd_delete_on_success = status[url]['postprocess_delete']
                    if vrd_delete_on_success == True:
                        # VideoReDo may hold the file open briefly
                        for attempt in range(30):
                            try:
                                os.remove(outfile)
                                break
                            except OSError:
                                time.sleep(1)
                        else:
                            logger.error('Unable to delete %s', outfile)

                    # Rename metadata file
                    if metafile != '' and os.path.isfile(metafile):
                        try:
                            new_metafile = vrd.output_file + '.txt'
                            os.rename(metafile, new_metafile)
                        except OSError:
                            logger.error('Error renaming metadata file')

                    status[url]['postprocessing'] = False
                    status[url]['finished'] = True
            else:
                status[url]['postprocessing'] = False
                status[url]['finished'] = True
                logger.info('Failed to initialize VideoReDo COM interface')


    def process_queue(self, tivoIP, mak, togo_path):
        PreventComputerFromSleeping(True)
        try:
            while queue[tivoIP]:
                url = queue[tivoIP][0]
                logger.debug('Processing %s', url)
                try:
                    self.get_tivo_file(tivoIP, url, mak, togo_path)
                except Exception:
                    logger.exception('Transfer of %s failed', url)
                    if url in status:
                        status[url].update({'running': False,
                                            'finished': True,
                                            'error': 'Transfer failed'})
                if queue[tivoIP] and queue[tivoIP][0] == url:
                    queue[tivoIP].pop(0)
        finally:
            del queue[tivoIP]
            if len(queue) == 0:
                PreventComputerFromSleeping(False)

    def ToGo(self, handler, query):
        togo_path = config.get_server('togo_path')
        for name, data in config.getShares():
            if togo_path == name:
                togo_path = data.get('path')
        if togo_path:
            tivoIP = query['TiVo'][0]
            tsn = config.tivos_by_ip(tivoIP)
            tivo_mak = config.get_tsn('tivo_mak', tsn)
            urls = query.get('Url', [])
            decode = 'decode' in query
            save = 'save' in query

            if 'postprocess' in query:
                postprocess = query['postprocess'][0]
            else:
                postprocess = config.get_server('vrd_post_processing')

            if 'postprocess_profile' in query:
                postprocess_profile = query['postprocess_profile'][0]
            else:
                postprocess_profile = config.get_server('vrd_profile', '')

            postprocess_decrypt = ('postprocess_decrypt' in query or
                                   config.get_server_bool('vrd_decrypt_qsf'))

            postprocess_delete = ('postprocess_delete' in query or
                                  config.get_server_bool('vrd_delete_on_success'))

            ts_format = 'ts_format' in query and config.is_ts_capable(tsn)
            for theurl in urls:
                if theurl in status:
                    del status[theurl]

                status[theurl] = {'running': False, 'status': '', 'error': '', 'rate': 0, 'percent': 0,
                                  'queued': True, 'size': 0, 'postprocessing': False, 'finished': False,
                                  'decode': decode, 'save': save, 'ts_format': ts_format,
                                  'postprocess': postprocess, 'postprocess_profile': postprocess_profile,
                                  'postprocess_decrypt': postprocess_decrypt, 'postprocess_delete': postprocess_delete,
                                  'retry': 0, 'ts_max_retries': int(config.get_server('togo_ts_max_retries', 0)),
                                  'ts_error_count': 0, 'best_file': '', 'best_error_count': 0, 'download_delay':0.0}
                if tivoIP in queue:
                    queue[tivoIP].append(theurl)
                else:
                    queue[tivoIP] = [theurl]
                    threading.Thread(target=self.process_queue,
                                     args=(tivoIP, tivo_mak, togo_path),
                                     name='togo-' + tivoIP,
                                     daemon=True).start()
                logger.info('[%s] Queued "%s" for transfer to %s' %
                            (time.strftime('%d/%b/%Y %H:%M:%S'),
                             unquote(theurl), togo_path))
            urlstring = '<br>'.join([html.escape(unquote(x)) for x in urls])
            message = TRANS_QUEUE % (urlstring, html.escape(togo_path))
        else:
            message = MISSING
        handler.redir(message, 5)

    def ToGoStop(self, handler, query):
        theurl = ''
        if 'Url' in query:
            theurl = query['Url'][0]
            if theurl in status:
                status[theurl]['running'] = False

        handler.redir(TRANS_STOP % html.escape(unquote(theurl)))

    def remove_from_queue(self, url, tivoIP):
        if url in status:
            if status[url]['running']:
                status[url]['running'] = False
            else:
                del status[url]

                if tivoIP in queue:
                    if url in queue[tivoIP]:
                        queue[tivoIP].remove(url)

                        logger.info('[%s] Removed "%s" from queue' %
                                    (time.strftime('%d/%b/%Y %H:%M:%S'),
                                     unquote(url)))


    def Unqueue(self, handler, query):
        theurl = ''

        if 'Url' in query:
            theurl = query['Url'][0]
            if 'TiVo' in query:
                tivoIP = query['TiVo'][0]

                self.remove_from_queue(theurl, tivoIP)

        handler.redir(UNQUEUE % html.escape(unquote(theurl)))


    def UnqueueAll(self, handler, query):
        for tivoIP in list(queue):
            for url in list(queue.get(tivoIP, [])):
                self.remove_from_queue(url, tivoIP)

        handler.send_json(json.dumps({'success': True}))
