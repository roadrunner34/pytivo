#!/usr/bin/env python3

import logging
import os
import platform
import sys
import time

if sys.version_info < (3, 9):
    print('ERROR: pyTivo requires Python 3.9 or newer\n')
    sys.exit(1)

import beacon
import config
import httpserver

def exceptionLogger(*args):
    sys.excepthook = sys.__excepthook__
    logging.getLogger('pyTivo').error('Exception in pyTivo', exc_info=args)

def last_date():
    lasttime = -1

    if getattr(sys, 'frozen', False):
        lasttime = os.path.getmtime(sys.executable)
    else:
        path = os.path.dirname(os.path.abspath(__file__))
        for root, dirs, files in os.walk(path):
            for name in files:
                if name.endswith('.py'):
                    tm = os.path.getmtime(os.path.join(root, name))
                    if tm > lasttime:
                        lasttime = tm

    return time.asctime(time.localtime(lasttime))

def setup(in_service=False):
    config.init(sys.argv[1:], in_service)
    config.init_logging()
    sys.excepthook = exceptionLogger

    port = config.getPort()

    httpd = httpserver.TivoHTTPServer(('', int(port)),
        httpserver.TivoHTTPHandler)

    logger = logging.getLogger('pyTivo')
    logger.info('Last modified: ' + last_date())
    logger.info('Python: ' + platform.python_version())
    logger.info('System: ' + platform.platform())

    for section, settings in config.getShares():
        httpd.add_container(section, settings)

    b = beacon.Beacon()
    b.add_service('TiVoMediaServer:%s/http' % port)
    b.start()
    if 'listen' in config.getBeaconAddresses():
        b.listen()

    httpd.set_beacon(b)
    httpd.set_service_status(in_service)

    logger.info('pyTivo is ready.')
    return httpd

def serve(httpd):
    try:
        httpd.serve_forever()
    except KeyboardInterrupt:
        pass

def mainloop():
    httpd = setup()
    serve(httpd)
    httpd.beacon.stop()
    httpd.server_close()
    return httpd.restart

if __name__ == '__main__':
    while mainloop():
        time.sleep(5)
