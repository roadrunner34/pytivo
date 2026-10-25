import os
import select
import sys
import time

import servicemanager
import win32event
import win32service
import win32serviceutil
import winerror

import pyTivo

class PyTivoService(win32serviceutil.ServiceFramework):
    _svc_name_ = 'pyTivo'
    _svc_display_name_ = 'pyTivo'
    _svc_deps_ = ["EventLog"]


    def __init__(self, args):
        win32serviceutil.ServiceFramework.__init__(self, args)
        self.stop_event = win32event.CreateEvent(None, 0, 0, None)

    def mainloop(self):
        httpd = pyTivo.setup(True)

        while True:
            sys.stdout.flush()
            (rx, tx, er) = select.select((httpd,), (), (), 5)
            for sck in rx:
                sck.handle_request()

            rc = win32event.WaitForSingleObject(self.stop_event, 5)
            if rc == win32event.WAIT_OBJECT_0 or httpd.stop:
                break

        httpd.beacon.stop()
        httpd.server_close()
        return httpd.restart

    def SvcDoRun(self):
        self.ReportServiceStatus(win32service.SERVICE_START_PENDING)

        if getattr(sys, 'frozen', False):
            p = os.path.join(os.environ['ALLUSERSPROFILE'], 'pyTivo')
            os.makedirs(p, exist_ok=True)
        else:
            p = os.path.dirname(os.path.abspath(__file__))

        try:
            f = open(os.path.join(p, 'log.txt'), 'w', encoding='utf-8',
                     buffering=1)
            sys.stdout = f
            sys.stderr = f
        except OSError:
            pass

        self.ReportServiceStatus(win32service.SERVICE_RUNNING)
        while self.mainloop():
            time.sleep(5)

        self.ReportServiceStatus(win32service.SERVICE_STOPPED)

    def SvcStop(self):
        self.ReportServiceStatus(win32service.SERVICE_STOP_PENDING)
        win32event.SetEvent(self.stop_event)

if __name__ == '__main__':
    if len(sys.argv) == 1:
        try:
            servicemanager.Initialize()
            servicemanager.PrepareToHostSingle(PyTivoService)
            servicemanager.StartServiceCtrlDispatcher()
        except win32service.error as details:
            if details.winerror == winerror.ERROR_FAILED_SERVICE_CONTROLLER_CONNECT:
                win32serviceutil.usage()
    else:
        win32serviceutil.HandleCommandLine(PyTivoService)
