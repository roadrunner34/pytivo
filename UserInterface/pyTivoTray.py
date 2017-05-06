import os
import sys
import wx
import threading
import time
import platform
import webbrowser
import ConfigParser
import urllib2
import socket
import json
from threading import Timer
from Icons import TrayIcon

versionString = '1.6.0'
version = versionString.split('.')

showDesktopOnStart = False
setToGoMAK = False
toGoMAK = ''
setToGoPath = False
toGoPath = ''
publishToGoPath = False
silentRun = False


isWindows = platform.system() == 'Windows'
isMacOSX = platform.system() == 'Darwin'
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
            if isMacOSX:
                configDir = os.path.dirname(os.path.dirname(os.path.dirname(os.path.dirname(sys.executable)))) # pyTivo tray is inside a .app bundle
            else:
                configDir = os.path.dirname(sys.executable)
        else:
            configDir = os.path.join(os.path.dirname(os.path.abspath(__file__)))
        configFile = os.path.join(configDir, 'pyTivo.conf')

    try:
        config.read(configFile)
    except:
        print 'No config file'

    return config


def SaveConfigFile(config):
    # always save to appdata if we can
    if os.environ['APPDATA']:
        appdataDir = os.path.join(os.environ['APPDATA'], 'pyTivo')
        if not os.path.exists(appdataDir):
            os.makedirs(appdataDir)

        configFile = os.path.join(appdataDir, 'pyTivo.conf')
    else:
        if getattr(sys, 'frozen', False):
            if isMacOSX:
                configDir = os.path.dirname(os.path.dirname(os.path.dirname(os.path.dirname(sys.executable)))) # pyTivo tray is inside a .app bundle
            else:
                configDir = os.path.dirname(sys.executable)
        else:
            configDir = os.path.join(os.path.dirname(os.path.abspath(__file__)))
        configFile = os.path.join(configDir, 'pyTivo.conf')

    file = open(configFile, 'w')
    config.write(file)
    file.close()


def GetPort():
    config = LoadConfigFile()
    if config.has_option('Server', 'port'):
        return config.get('Server', 'port')

    return '9032'  # default port


def GetUpdateCheckInterval():
    config = LoadConfigFile()
    if config.has_option('Server', 'update_check_interval'):
        try:
            return int(config.get('Server', 'update_check_interval'))
        except ValueError:
            return 1

    return 1  # default is once a day


def SetUpdateCheckInterval(days):
    config = LoadConfigFile()
    if not config.has_section('Server'):
        config.add_section('Server')

    config.set('Server', 'update_check_interval', days)
    SaveConfigFile(config)


def ShowWebUI():
    webbrowser.open('http://localhost:' + GetPort(), new=0, autoraise=True)


def SetTiVoMAK(mak):
    config = LoadConfigFile()
    if not config.has_section('Server'):
        config.add_section('Server')

    config.set('Server', 'tivo_mak', mak)
    SaveConfigFile(config)

def SetToGoPath(path):
    config = LoadConfigFile()
    if not config.has_section('Server'):
        config.add_section('Server')

    config.set('Server', 'togo_path', path)
    SaveConfigFile(config)

def PublishToGoPath(path):
    config = LoadConfigFile()
    folderName = os.path.basename(path)

    count = 2
    while True:
        if config.has_section(folderName):
            if config.get(folderName, 'path') != path:
                folderName = os.path.basename(path) + ' %d' % count
                count += 1
                continue
        break

    if not config.has_section(folderName):
        config.add_section(folderName)

    config.set(folderName, 'path', path)
    config.set(folderName, 'type', 'video')
    SaveConfigFile(config)

def GetDownloadQueueCount():
    try:
        response = json.load(urllib2.urlopen('http://localhost:' + GetPort() + '/TiVoConnect?Command=GetTotalQueueCount&Container=ToGo'))

        if 'count' in response:
            return int(response['count'])
        else:
            return 0
    except:
        return 0

def GetUploadQueueCount():
    try:
        response = json.load(urllib2.urlopen('http://localhost:' + GetPort() + '/TiVoConnect?Command=GetActiveTransferCount'))

        if 'count' in response:
            return int(response['count'])
        else:
            return 0
    except:
        return 0

def CancelAllTransfers():
    try:
        urllib2.urlopen('http://localhost:' + GetPort() + '/TiVoConnect?Command=UnqueueAll&Container=ToGo')
    except:
        pass


class pyTivoTray(wx.TaskBarIcon):
    def __init__(self, frame):
        if isWindows:
            self.mutexName = 'pyTivoTray_{BF213038-4019-49C0-A0AD-9D4419852647}'
            self.mutex = CreateMutex(None, False, self.mutexName)
            if (GetLastError() == ERROR_ALREADY_EXISTS):
                sys.exit()

        if setToGoMAK:
            SetTiVoMAK(toGoMAK)

        if setToGoPath:
            SetToGoPath(toGoPath)
            if publishToGoPath:
                PublishToGoPath(toGoPath)

        if silentRun: # Just want to set the MAK and ToGo Paths then exit
            sys.exit()

        self.frame = frame
        super(pyTivoTray, self).__init__()
        self.isPyTivoRunning = False
        self.UpdateIcon()
        self.versionCheckTimer = None
        self.pyTivoThread = None
        self.StartPyTivoThread()

        self.Bind(wx.EVT_TASKBAR_LEFT_DCLICK, self.OnOpenDesktop)
        self.CheckVersion(True)

        if showDesktopOnStart:
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
        CreateMenuItem(menu, 'Check for update', self.OnCheckVersion)
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
            cliOptions = '/setport/' + GetPort()
            if isMacOSX:
                pyTivoDesktopPath = os.path.join(os.path.dirname(os.path.dirname(os.path.dirname(os.path.dirname(sys.executable)))), 'pyTivoDesktop.app')
                pyTivoDesktopProcess = subprocess.Popen('open -a ' + pyTivoDesktopPath + ' --args ' + cliOptions, shell=True)
            elif isWindows:
                pyTivoDesktopPath = os.path.join(os.path.dirname(sys.executable), 'desktop', 'pyTivoDesktop')
                pyTivoDesktopProcess = subprocess.Popen('\"' + pyTivoDesktopPath + '\" ' + cliOptions, shell=True)
        else:
            parentDirectory = os.path.dirname(os.path.dirname(os.path.dirname(os.path.abspath(__file__))))
            pyTivoDesktopProcess = subprocess.Popen(
                os.path.join(parentDirectory, 'build\desktop\pyTivoDesktop  /setport/') + GetPort(), shell=True)

    def CheckVersion(self, silentCheck):
        self.checkVersionThread = threading.Thread(target=self.CheckVersionFunc, args=(silentCheck,))
        self.checkVersionThread.start()

    def CheckVersionFunc(self, silentCheck):
        isError = False
        try:
            if isMacOSX:
                response = urllib2.urlopen('http://www.pytivodesktop.com/mac/version.info').read()
            elif isWindows:
                response = urllib2.urlopen('http://www.pytivodesktop.com/win32/version.info').read()
            else:
                return

            latest = response.split('.')

            newer = False
            if int(latest[0]) > int(version[0]):
                newer = True
            elif int(latest[0]) == int(version[0]):
                if int(latest[1]) > int(version[1]):
                    newer = True
                elif int(latest[1]) == int(version[1]):
                    if int(latest[2]) > int(version[2]):
                        newer = True

            if newer:
                newVersion = latest[0] + '.' + latest[1] + '.' + latest[2]

                dlg = wx.MessageDialog(None,
                                       'A new version of pyTivo Desktop is available',
                                       'Update Available', wx.YES_NO | wx.ICON_INFORMATION | wx.YES_DEFAULT | wx.CENTRE)

                currentText = 'Current version: ' + version[0] + '.' + version[1] + '.' + version[2]
                newText = 'Current version: ' + newVersion
                extMessageText =  currentText + '\n' + newText + '\n\nWould you like to download it now?'
                dlg.SetExtendedMessage(extMessageText)

                doDownload = dlg.ShowModal() == wx.ID_YES
                dlg.Destroy()

                if doDownload:
                    if isMacOSX:
                        webbrowser.open('http://www.pytivodesktop.com/mac.html', new=2, autoraise=True)
                    elif isWindows:
                        webbrowser.open('http://www.pytivodesktop.com/windows.html', new=2, autoraise=True)

            elif not silentCheck:
                dlg = wx.MessageDialog(None,
                                       'Your version of pyTivo Desktop is current',
                                       'No Update Available', wx.OK | wx.ICON_INFORMATION | wx.CENTRE)

                dlg.ShowModal()
                dlg.Destroy()

        except urllib2.URLError, e:
            isError = True
        except socket.timeout, e:
            isError = True

        if isError:
            if not silentCheck:
                dlg = wx.MessageDialog(None,
                                       'Error contacting server, try again later',
                                       'Error', wx.OK | wx.ICON_ERROR | wx.CENTRE)

                dlg.ShowModal()
                dlg.Destroy()

        # Kill existing timer if it's still active
        if self.versionCheckTimer != None:
            if self.versionCheckTimer.is_alive():
                self.versionCheckTimer.cancel()
                self.versionCheckTimer = None

        # Check user setting for update check interval
        updateInterval = GetUpdateCheckInterval()
        if updateInterval > 0:
            seconds = updateInterval * 86400 # 86400 = seconds in a day
            self.versionCheckTimer = Timer(float(seconds), self.CheckVersion, args=[True])
            self.versionCheckTimer.start()

    def StartPyTivoThread(self):
        if self.isPyTivoRunning or self.pyTivoThread != None:
            return

        self.pyTivoStop = threading.Event()
        self.pyTivoThread = threading.Thread(target=self.pyTivoThreadFunc)
        self.pyTivoThread.start()

    def StopPyTivoThread(self):
        if self.isPyTivoRunning:
            uploadCount = GetUploadQueueCount()
            downloadCount = GetDownloadQueueCount()
            message = ''
            if downloadCount > 0 and uploadCount == 0:
                message = 'There are still %d recordings being downloaded, are you sure you want to stop pyTivo?' % downloadCount
            elif uploadCount > 0 and downloadCount == 0:
                message = 'There are still %d videos being uploaded, are you sure you want to stop pyTivo?' % uploadCount
            elif downloadCount > 0 and uploadCount > 0:
                message = 'There are still %d recordings being downloaded and %d videos being uploaded, are you sure you want to stop pyTivo?' % (downloadCount, uploadCount)

            if downloadCount > 0 or uploadCount > 0:
                dlg = wx.MessageDialog(None, message, 'Confirm',
                                       wx.YES_NO | wx.ICON_EXCLAMATION | wx.NO_DEFAULT | wx.CENTRE)

                stopPyTivo = dlg.ShowModal() == wx.ID_YES
                dlg.Destroy()
                if not stopPyTivo:
                    return False
                else:
                    CancelAllTransfers()

            self.pyTivoStop.set()
            self.pyTivoThread.join()
            return True

        return True

    def RestartPyTivo(self):
        urllib2.urlopen('http://localhost:' + GetPort() + '/TiVoConnect?Command=Restart&Container=Settings').read()

    def pyTivoThreadFunc(self):
        import subprocess

        if getattr(sys, 'frozen', False):
            if isMacOSX:
                pyTivoProcess = subprocess.Popen(os.path.join(os.path.dirname(sys.executable), 'pyTivo'), shell=True)
            elif isWindows:
                pyTivoProcess = subprocess.Popen(os.path.join(os.path.dirname(sys.executable), 'pyTivo'), shell=True)
            else:
                return
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
        self.pyTivoThread = None
        self.UpdateIcon()

    def OnOpenDesktop(self, event):
        self.OpenDesktop()

    def OnStartPyTivoThread(self, event):
        self.StartPyTivoThread()

    def OnStopPyTivoThread(self, event):
        self.StopPyTivoThread()

    def OnRestartPyTivo(self, event):
        self.RestartPyTivo()

    def OnCheckVersion(self, event):
        self.CheckVersion(False)

    def OnExit(self, event):
        # Confirm user wants to exit
        dlg = wx.MessageDialog(None, 'Are you sure you want to exit pyTivo?', 'Confirm',
                               wx.YES_NO | wx.ICON_EXCLAMATION | wx.NO_DEFAULT | wx.CENTRE)
        exitPyTivo = dlg.ShowModal() == wx.ID_YES
        dlg.Destroy()
        if not exitPyTivo:
            return

        if self.StopPyTivoThread():
            # Kill version check timer
            if self.versionCheckTimer != None:
                if self.versionCheckTimer.is_alive():
                    self.versionCheckTimer.cancel()
                    self.versionCheckTimer = None

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
    for arg in sys.argv:
        if arg == '--show-desktop':
            showDesktopOnStart = True
        elif arg == '--mak':
            setToGoMAK = True
        elif arg == '--path':
            setToGoPath = True
        elif arg == '--publish':
            publishToGoPath = True
        elif arg == '--silent':
            silentRun = True
        else:
            if setToGoMAK and not toGoMAK:
                toGoMAK = arg
            elif setToGoPath and not toGoPath:
                toGoPath = arg


    main()