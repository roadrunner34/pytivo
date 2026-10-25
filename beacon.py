import logging
import re
import socket
import struct
import threading
import time
import uuid
from urllib.parse import quote

import zeroconf

import config
from plugin import GetPlugin

SHARE_TEMPLATE = '/TiVoConnect?Command=QueryContainer&Container=%s'
DESKTOP_URL = '/Desktop'
PLATFORM_MAIN = 'pyTivo'
PLATFORM_VIDEO = 'pc/pyTivo'    # For the nice icon

def _text(value):
    if isinstance(value, bytes):
        return value.decode('utf-8', 'replace')
    return value

class ZCListener(zeroconf.ServiceListener):
    def __init__(self, names):
        self.names = names

    def remove_service(self, server, type_, name):
        short = name.replace('.' + type_, '')
        if short in self.names:
            self.names.remove(short)

    def add_service(self, server, type_, name):
        self.names.append(name.replace('.' + type_, ''))

    def update_service(self, server, type_, name):
        pass

class ZCBroadcast:
    def __init__(self, logger):
        """ Announce our shares via Zeroconf. """
        self.share_names = []
        self.share_info = []
        self.logger = logger
        self.rz = zeroconf.Zeroconf(ip_version=zeroconf.IPVersion.V4Only)
        self.renamed = {}
        old_titles = self.scan()
        address = socket.inet_aton(config.get_ip())
        port = int(config.getPort())
        server = socket.gethostname().split('.')[0] + '.local.'

        """ Announce Desktop via Zeroconf. """
        desc = {'path': DESKTOP_URL,
                'platform': PLATFORM_MAIN, 'protocol': 'http'}
        info = zeroconf.ServiceInfo('_pytivo._tcp.local.',
                                    'pyTivo Desktop._pytivo._tcp.local.',
                                    addresses=[address], port=port,
                                    properties=desc, server=server)
        self.rz.register_service(info, allow_name_change=True)
        self.share_info.append(info)

        """ Announce our shares via Zeroconf. """
        logger.info('Announcing shares...')
        for section, settings in config.getShares():
            try:
                ct = GetPlugin(settings['type']).CONTENT_TYPE
            except Exception:
                continue
            if ct.startswith('x-container/'):
                if 'video' in ct:
                    platform = PLATFORM_VIDEO
                else:
                    platform = PLATFORM_MAIN
                logger.info('Registering: %s' % section)
                self.share_names.append(section)
                desc = {'path': SHARE_TEMPLATE % quote(section),
                        'platform': platform, 'protocol': 'http',
                        'tsn': '{%s}' % uuid.uuid4()}
                tt = ct.split('/')[1]
                title = section
                count = 1
                while title in old_titles:
                    count += 1
                    title = '%s [%d]' % (section, count)
                    self.renamed[section] = title
                info = zeroconf.ServiceInfo('_%s._tcp.local.' % tt,
                    '%s._%s._tcp.local.' % (title, tt),
                    addresses=[address], port=port, properties=desc,
                    server=server)
                self.rz.register_service(info)
                self.share_info.append(info)

    def scan(self):
        """ Look for TiVos using Zeroconf. """
        VIDS = '_tivo-videos._tcp.local.'
        names = []

        self.logger.info('Scanning for TiVos...')

        # Get the names of servers offering TiVo videos
        browser = zeroconf.ServiceBrowser(self.rz, VIDS,
                                          listener=ZCListener(names))

        # Give them up to 30 seconds to respond
        waitSeconds = 0
        while not names and waitSeconds < 30:
            time.sleep(1)
            waitSeconds += 1
        browser.cancel()

        # Any results?
        if names:
            config.tivos_found = True
        else:
            self.logger.warning('No TiVos found!')

        # Now get the addresses -- this is the slow part
        for name in list(names):
            info = self.rz.get_service_info(VIDS, name + '.' + VIDS)
            if not info:
                continue
            properties = {_text(k): _text(v)
                          for k, v in info.properties.items()}
            tsn = properties.get('TSN')
            if config.get_server('togo_all'):
                tsn = properties.get('tsn', tsn)
            addresses = info.parsed_addresses(zeroconf.IPVersion.V4Only)
            if tsn and addresses:
                address = addresses[0]
                config.tivos[tsn] = {'name': name, 'address': address,
                                     'port': info.port}
                config.tivos[tsn].update(properties)
                self.logger.info(name + ' - ' + address)

        return names

    def shutdown(self):
        self.logger.info('Unregistering: %s' % ' '.join(self.share_names))
        for info in self.share_info:
            self.rz.unregister_service(info)
        self.rz.close()

class Beacon:
    def __init__(self):
        self.UDPSock = socket.socket(socket.AF_INET, socket.SOCK_DGRAM)
        self.UDPSock.setsockopt(socket.SOL_SOCKET, socket.SO_BROADCAST, 1)
        self.services = []
        self.timer = None
        self.logger = logging.getLogger('pyTivo.beacon')

        self.platform = PLATFORM_VIDEO
        for section, settings in config.getShares():
            try:
                ct = GetPlugin(settings['type']).CONTENT_TYPE
            except Exception:
                continue
            if ct in ('x-container/tivo-music', 'x-container/tivo-photos'):
                self.platform = PLATFORM_MAIN
                break

        self.bd = None
        if config.get_zc():
            try:
                self.bd = ZCBroadcast(self.logger)
            except Exception:
                self.logger.exception('Zeroconf failure')

    def add_service(self, service):
        self.services.append(service)
        self.send_beacon()

    def format_services(self):
        return ';'.join(self.services)

    def format_beacon(self, conntype, services=True):
        beacon = ['tivoconnect=1',
                  'method=%s' % conntype,
                  'identity={%s}' % config.getGUID(),
                  'machine=%s' % socket.gethostname(),
                  'platform=%s' % self.platform]

        if services:
            beacon.append('services=' + self.format_services())
        else:
            beacon.append('services=TiVoMediaServer:0/http')

        return ('\n'.join(beacon) + '\n').encode('utf-8')

    def send_beacon(self):
        beacon_ips = config.getBeaconAddresses()
        beacon = self.format_beacon('broadcast')
        for beacon_ip in beacon_ips.split():
            if beacon_ip != 'listen':
                try:
                    packet = beacon
                    while packet:
                        result = self.UDPSock.sendto(packet, (beacon_ip, 2190))
                        if result < 0:
                            break
                        packet = packet[result:]
                except OSError as e:
                    self.logger.info('Beacon to %s failed: %s', beacon_ip, e)

    def start(self):
        self.send_beacon()
        self.timer = threading.Timer(60, self.start)
        self.timer.daemon = True
        self.timer.start()

    def stop(self):
        if self.timer:
            self.timer.cancel()
        if self.bd:
            self.bd.shutdown()

    def recv_bytes(self, sock, length):
        block = b''
        while len(block) < length:
            add = sock.recv(length - len(block))
            if not add:
                break
            block += add
        return block

    def recv_packet(self, sock):
        length = struct.unpack('!I', self.recv_bytes(sock, 4))[0]
        return self.recv_bytes(sock, length)

    def send_packet(self, sock, packet):
        sock.sendall(struct.pack('!I', len(packet)) + packet)

    def listen(self):
        """ For the direct-connect, TCP-style beacon """

        def server():
            TCPSock = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
            TCPSock.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
            TCPSock.bind(('', 2190))
            TCPSock.listen(5)

            while True:
                # Wait for a connection
                client, address = TCPSock.accept()
                try:
                    # Accept (and discard) the client's beacon
                    self.recv_packet(client)

                    # Send ours
                    self.send_packet(client, self.format_beacon('connected'))
                except (OSError, struct.error) as msg:
                    self.logger.info('Beacon exchange with %s failed: %s',
                                     address[0], msg)
                finally:
                    client.close()

        threading.Thread(target=server, name='beacon-listen',
                         daemon=True).start()

    def get_name(self, address):
        """ Exchange beacons, and extract the machine name. """
        our_beacon = self.format_beacon('connected', False)
        machine_name = re.compile('machine=(.*)\n').search

        try:
            tsock = socket.create_connection((address, 2190), timeout=5)
            try:
                self.send_packet(tsock, our_beacon)
                tivo_beacon = self.recv_packet(tsock).decode('utf-8',
                                                             'replace')
            finally:
                tsock.close()
            name = machine_name(tivo_beacon).groups()[0]
        except (OSError, struct.error, AttributeError):
            name = address

        return name
