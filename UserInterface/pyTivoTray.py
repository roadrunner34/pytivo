import os
import sys
import wx
import threading
import time
import platform
import webbrowser
import ConfigParser
import urllib2
from Icons import TrayIcon

showDesktopOnStart = False

isWindows = platform.system() == 'Windows'
if isWindows:
    from win32event import CreateMutex
    from win32api import GetLastError, CloseHandle
    from winerror import ERROR_ALREADY_EXISTS


def CreateMenuItem(menu, label, func):
    item = wx.MenuItem(menu, -1, label)
    menu.Bind(wx.EVT_MENU, func, id=item.GetId())
    menu.AppendItem(item)
    return item


def LoadConfigFile():
    config = ConfigParser.ConfigParser()

    # load config file
    configFile = ''
    if 'APPDATA' in os.environ:
        configFile = os.path.join(os.environ['APPDATA'], 'pyTivo', 'pyTivo.conf')

    if not os.path.isfile(configFile):
        if getattr(sys, 'frozen', False):
            configDir = os.path.dirname(sys.executable)
        else:
            configDir = os.path.join(os.path.dirname(os.path.abspath(__file__)))
        configFile = os.path.join(configDir, 'pyTivo.conf')

    try:
        config.read(configFile)
    except:
        print 'No config file'

    return config


def GetPort():
    config = LoadConfigFile()
    if config.has_option('Server', 'port'):
        return config.get('Server', 'port')

    return '9032'  # default port


def ShowWebUI():
    webbrowser.open('http://localhost:' + GetPort(), new=0, autoraise=True)


class pyTivoTray(wx.TaskBarIcon):
    def __init__(self, frame):
        if isWindows:
            self.mutexName = 'pyTivoTray_{BF213038-4019-49C0-A0AD-9D4419852647}'
            self.mutex = CreateMutex(None, False, self.mutexName)
            if (GetLastError() == ERROR_ALREADY_EXISTS):
                sys.exit()

        self.frame = frame
        super(pyTivoTray, self).__init__()
        self.isPyTivoRunning = False
        self.UpdateIcon()
        self.pyTivoThread = None
        self.StartPyTivoThread()

        self.Bind(wx.EVT_TASKBAR_LEFT_DCLICK, self.OnOpenDesktop)

        if (showDesktopOnStart):
            self.OpenDesktop()


    def CreatePopupMenu(self):
        menu = wx.Menu()

        CreateMenuItem(menu, 'Open pyTivo Desktop', self.OnOpenDesktop)

        menu.AppendSeparator()

        if self.isPyTivoRunning:
            CreateMenuItem(menu, 'Stop pyTivo', self.OnStopPyTivoThread)
        else:
            CreateMenuItem(menu, 'Start pyTivo', self.OnStartPyTivoThread)

        item = CreateMenuItem(menu, 'Restart pyTivo', self.OnRestartPyTivo)
        item.Enable(self.isPyTivoRunning)

        menu.AppendSeparator()
        CreateMenuItem(menu, 'Exit', self.OnExit)
        return menu

    def UpdateIcon(self):
        icon = TrayIcon()
        if self.isPyTivoRunning:
            self.SetIcon(icon.GetRunning(), 'pyTivo\n(running)')
        else:
            self.SetIcon(icon.GetStopped(), 'pyTivo\n(stopped)')

    def OpenDesktop(self):
        if not self.isPyTivoRunning:
            self.StartPyTivoThread()

        import subprocess
        if getattr(sys, 'frozen', False):
            pyTivoDesktopPath = os.path.join(os.path.dirname(sys.executable), 'desktop\pyTivoDesktop')
            cliOptions = '/setport/' + GetPort()
            pyTivoDesktopProcess = subprocess.Popen('\"' + pyTivoDesktopPath + '\" ' + cliOptions, shell=True)
        else:
            parentDirectory = os.path.dirname(os.path.dirname(os.path.dirname(os.path.abspath(__file__))))
            pyTivoDesktopProcess = subprocess.Popen(
                os.path.join(parentDirectory, 'build\desktop\pyTivoDesktop  /setport/') + GetPort(), shell=True)

    def StartPyTivoThread(self):
        if self.isPyTivoRunning:
            return

        self.pyTivoStop = threading.Event()
        self.pyTivoThread = threading.Thread(target=self.pyTivoThreadFunc)
        self.pyTivoThread.start()

    def StopPyTivoThread(self):
        if self.isPyTivoRunning:
            self.pyTivoStop.set()
            self.pyTivoThread.join()

    def RestartPyTivo(self):
        urllib2.urlopen('http://localhost:' + GetPort() + '/TiVoConnect?Command=Restart&Container=Settings').read()

    def pyTivoThreadFunc(self):
        import subprocess
        import signal

        if getattr(sys, 'frozen', False):
            pyTivoProcess = subprocess.Popen(os.path.join(os.path.dirname(sys.executable), 'pyTivo'), shell=True)
        else:
            pyTivoProcess = subprocess.Popen(
                'python ' + os.path.join(os.path.dirname(os.path.dirname(os.path.abspath(__file__))), 'pyTivo.py'), shell=True)

        self.isPyTivoRunning = True
        self.UpdateIcon()

        while pyTivoProcess.poll() is None:
            time.sleep(1.0)

            if (self.pyTivoStop.isSet()):
                urllib2.urlopen('http://localhost:' + GetPort() + '/TiVoConnect?Command=Quit&Container=Settings').read()
                break

        self.isPyTivoRunning = False
        self.UpdateIcon()

    def OnOpenDesktop(self, event):
        self.OpenDesktop()

    def OnStartPyTivoThread(self, event):
        self.StartPyTivoThread()

    def OnStopPyTivoThread(self, event):
        self.StopPyTivoThread()

    def OnRestartPyTivo(self, event):
        self.RestartPyTivo();

    def OnExit(self, event):
        # Confirm user wants to exit
        dlg = wx.MessageDialog(None, 'Are you sure you want to exit pyTivo?', 'Confirm',
                               wx.YES_NO | wx.ICON_EXCLAMATION | wx.NO_DEFAULT | wx.CENTRE)
        exitPyTivo = dlg.ShowModal() == wx.ID_YES
        dlg.Destroy()
        if not exitPyTivo:
            return

        if self.isPyTivoRunning:
            self.pyTivoStop.set()
            self.pyTivoThread.join()

        wx.CallAfter(self.Destroy)
        self.frame.Close()

    def __del__(self):
        if isWindows:
            if self.mutex:
                CloseHandle(self.mutex)


class pyTivoApp(wx.App):
    def OnInit(self):
        frame = wx.Frame(None)
        self.SetTopWindow(frame)
        pyTivoTray(frame)
        return True

def main():
    app = pyTivoApp(False)
    app.MainLoop()


if __name__ == '__main__':
    if len(sys.argv) > 1:
        if sys.argv[1] == '--show-desktop':
            showDesktopOnStart = True

    main()