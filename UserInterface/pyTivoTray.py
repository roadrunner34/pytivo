import configparser
import json
import os
import platform
import subprocess
import sys
import threading
import time
import urllib.error
import urllib.request
import webbrowser
from threading import Timer

import wx
import wx.adv

try:
    from UserInterface.Icons import TrayIcon
except ImportError:
    from Icons import TrayIcon

versionString = '1.6.26'
version = versionString.split('.')

showDesktopOnStart = False
isWindows = platform.system() == 'Windows'
isMacOSX = platform.system() == 'Darwin'
if isWindows:
    from win32event import CreateMutex
    from win32api import GetLastError, CloseHandle
    from winerror import ERROR_ALREADY_EXISTS
    import win32service
    import win32serviceutil


URL_TIMEOUT = 10  # seconds


def UrlRead(url):
    with urllib.request.urlopen(url, timeout=URL_TIMEOUT) as response:
        return response.read().decode('utf-8', 'replace').strip()


def LocalCommand(command, container):
    return UrlRead('http://localhost:%s/TiVoConnect?Command=%s&Container=%s'
                   % (GetPort(), command, container))


def AppDir():
    """Directory holding the frozen app (outside the .app bundle on Mac)."""
    if isMacOSX:
        # pyTivo tray is inside a .app bundle
        return os.path.dirname(os.path.dirname(os.path.dirname(
            os.path.dirname(sys.executable))))
    return os.path.dirname(sys.executable)


def CreateMenuItem(menu, label, func):
    item = wx.MenuItem(menu, -1, label)
    menu.Bind(wx.EVT_MENU, func, id=item.GetId())
    menu.Append(item)
    return item


def LoadConfigFile():
    config = configparser.ConfigParser(interpolation=None)

    # load config file
    configFile = ''
    if isWindows:
        status = None
        try:
            status = win32serviceutil.QueryServiceStatus('pyTivo')
        except Exception:
            if 'APPDATA' in os.environ:
                configFile = os.path.join(os.environ['APPDATA'], 'pyTivo', 'pyTivo.conf')
        else:
            if 'ALLUSERSPROFILE' in os.environ:
                configFile = os.path.join(os.environ['ALLUSERSPROFILE'], 'pyTivo', 'pyTivo.conf')

    if not os.path.isfile(configFile):
        if getattr(sys, 'frozen', False):
            configDir = AppDir()
        else:
            # pyTivo.conf lives next to pyTivo.py
            configDir = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
        configFile = os.path.join(configDir, 'pyTivo.conf')

    try:
        config.read(configFile, encoding='utf-8')
    except (configparser.Error, UnicodeDecodeError):
        print('Unable to read config file ' + configFile)

    return config


def SaveConfigFile(config):
    configFile = ''
    if isWindows:
        status = None
        try:
            status = win32serviceutil.QueryServiceStatus('pyTivo')
        except Exception:
            appdataDir = os.path.join(os.environ['APPDATA'], 'pyTivo')
            if not os.path.exists(appdataDir):
                os.makedirs(appdataDir)

            configFile = os.path.join(appdataDir, 'pyTivo.conf')
        else:
            appdataDir = os.path.join(os.environ['ALLUSERSPROFILE'], 'pyTivo')
            if not os.path.exists(appdataDir):
                os.makedirs(appdataDir)

            configFile = os.path.join(appdataDir, 'pyTivo.conf')


    if not configFile:
        if getattr(sys, 'frozen', False):
            configDir = AppDir()
        else:
            configDir = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))

        configFile = os.path.join(configDir, 'pyTivo.conf')

    with open(configFile, 'w', encoding='utf-8') as file:
        config.write(file)


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
            pass

    return 1  # default is once a day


def GetUpdateIgnoreVersion():
    ignoreVersion = '0.0.0'
    config = LoadConfigFile()
    if config.has_option('Server', 'update_check_ignore_version'):
        try:
            ignoreVersion = config.get('Server', 'update_check_ignore_version')
        except ValueError:
            pass

    return ignoreVersion.split('.')


def SetUpdateIgnoreVersion(ignoreVersion):
    config = LoadConfigFile()
    if not config.has_section('Server'):
        config.add_section('Server')

    config.set('Server', 'update_check_ignore_version', ignoreVersion)
    SaveConfigFile(config)


def SetUpdateCheckInterval(days):
    config = LoadConfigFile()
    if not config.has_section('Server'):
        config.add_section('Server')

    config.set('Server', 'update_check_interval', str(days))
    SaveConfigFile(config)


def ShowWebUI():
    webbrowser.open('http://localhost:' + GetPort(), new=0, autoraise=True)

def IsBetaTester():
    config = LoadConfigFile()
    try:
        beta_tester = config.getboolean('Server', 'beta_tester')
    except (configparser.Error, ValueError):
        beta_tester = False
    return beta_tester

def GetDownloadQueueCount():
    try:
        response = json.loads(LocalCommand('GetTotalQueueCount', 'ToGo'))
        return int(response.get('count', 0))
    except (OSError, ValueError):
        return 0

def GetUploadQueueCount():
    try:
        response = json.loads(UrlRead('http://localhost:' + GetPort() +
                              '/TiVoConnect?Command=GetActiveTransferCount'))
        return int(response.get('count', 0))
    except (OSError, ValueError):
        return 0


def CancelAllTransfers():
    try:
        LocalCommand('UnqueueAll', 'ToGo')
    except OSError:
        pass


def CheckUserIsAdmin():
    if isWindows:
        import ctypes
        try:
            return ctypes.windll.shell32.IsUserAnAdmin()
        except (AttributeError, OSError):
            print('Admin check failed, assuming not an admin.')
            return False

    return False


def RestartAsAdmin():
    if isWindows:
        import ctypes
        params = ''
        if showDesktopOnStart:
            params = '--show-desktop'

        if getattr(sys, 'frozen', False):
            ctypes.windll.shell32.ShellExecuteW(None, 'runas', sys.executable, params, None, 1)
        else:
            ctypes.windll.shell32.ShellExecuteW(None, 'runas', sys.executable, '\"'+ __file__+'\" ' + params, None, 1)


class pyTivoTray(wx.adv.TaskBarIcon):
    def __init__(self, frame):
        self.mutex = None
        if isWindows:
            self.mutexName = 'pyTivoTray_{BF213038-4019-49C0-A0AD-9D4419852647}'
            self.mutex = CreateMutex(None, False, self.mutexName)
            if (GetLastError() == ERROR_ALREADY_EXISTS):
                print('pyTivoTray already running')
                sys.exit()

        self.isPyTivoService = False
        if isWindows:
            status = None
            try:
                status = win32serviceutil.QueryServiceStatus('pyTivo')
            except Exception:
                pass # Service not installed
            else:
                self.isPyTivoService = True
                if not CheckUserIsAdmin():
                    print('Must be run elevated to control service')

                    if self.mutex:
                        CloseHandle(self.mutex)

                    RestartAsAdmin()
                    sys.exit()

        self.frame = frame
        super(pyTivoTray, self).__init__()

        self.isPyTivoRunning = False
        self.UpdateIcon()
        self.versionCheckTimer = None

        if self.isPyTivoService:
            self.StartPyTivoService()
            self.StartPyTivoServiceMonitorThread()
        else:
            self.pyTivoThread = None
            self.StartPyTivoThread()

        self.Bind(wx.adv.EVT_TASKBAR_LEFT_DCLICK, self.OnOpenDesktop)
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
        # wx may only be touched from the GUI thread
        if not wx.IsMainThread():
            wx.CallAfter(self.UpdateIcon)
            return

        icon = TrayIcon()
        if self.isPyTivoRunning:
            self.SetIcon(icon.GetRunning(), 'pyTivo\n(running)')
        else:
            self.SetIcon(icon.GetStopped(), 'pyTivo\n(stopped)')


    def OpenDesktop(self):
        if not self.isPyTivoRunning:
            self.StartPyTivoThread()

        cliOptions = '/setport/' + GetPort()
        try:
            if getattr(sys, 'frozen', False):
                if isMacOSX:
                    pyTivoDesktopPath = os.path.join(AppDir(), 'pyTivoDesktop.app')
                    subprocess.Popen(['open', '-a', pyTivoDesktopPath, '--args', cliOptions])
                elif isWindows:
                    pyTivoDesktopPath = os.path.join(os.path.dirname(sys.executable), 'desktop', 'pyTivoDesktop')
                    subprocess.Popen([pyTivoDesktopPath, cliOptions])
            else:
                parentDirectory = os.path.dirname(os.path.dirname(os.path.dirname(os.path.abspath(__file__))))
                pyTivoDesktopPath = os.path.join(parentDirectory, 'build', 'desktop', 'pyTivoDesktop')
                if os.path.exists(pyTivoDesktopPath) or os.path.exists(pyTivoDesktopPath + '.exe'):
                    subprocess.Popen([pyTivoDesktopPath, cliOptions])
                else:
                    # No desktop app when running from source; use the web UI
                    ShowWebUI()
        except OSError as msg:
            print('Unable to open pyTivo Desktop: %s' % msg)
            ShowWebUI()


    def CheckVersion(self, silentCheck):
        self.checkVersionThread = threading.Thread(target=self.CheckVersionFunc, args=(silentCheck,))
        self.checkVersionThread.start()


    def PromptVersionUpdate(self, updateAvailable, newVersion, error):
        if updateAvailable:
            dlg = wx.MessageDialog(None,
                                   'A new version of pyTivo Desktop is available',
                                   'Update Available', wx.YES_NO | wx.ICON_INFORMATION | wx.YES_DEFAULT | wx.CENTRE | wx.CANCEL)

            currentText = 'Current version: ' + version[0] + '.' + version[1] + '.' + version[2]
            newText = 'New version: ' + newVersion
            extMessageText = currentText + '\n' + newText + '\n\nWould you like to download it now?'
            dlg.SetExtendedMessage(extMessageText)
            dlg.SetYesNoCancelLabels('Download Now', 'Maybe Later', 'Ignore Update')

            answer = dlg.ShowModal()
            doDownload = answer == wx.ID_YES
            doIgnore = answer == wx.ID_CANCEL
            dlg.Destroy()

            if doDownload:
                if isMacOSX:
                    webbrowser.open('http://www.pytivodesktop.com/mac.html', new=2, autoraise=True)
                elif isWindows:
                    webbrowser.open('http://www.pytivodesktop.com/windows.html', new=2, autoraise=True)
            elif doIgnore:
                SetUpdateIgnoreVersion(newVersion)

        elif error:
            dlg = wx.MessageDialog(None,
                                   'Error contacting server, try again later',
                                   'Error', wx.OK | wx.ICON_ERROR | wx.CENTRE)

            dlg.ShowModal()
            dlg.Destroy()
        else:
            dlg = wx.MessageDialog(None,
                                   'Your version of pyTivo Desktop is current',
                                   'No Update Available', wx.OK | wx.ICON_INFORMATION | wx.CENTRE)

            dlg.ShowModal()
            dlg.Destroy()



    def PromptBetaUpdate(self, updateAvailable, newVersion, error):
        if updateAvailable:
            dlg = wx.MessageDialog(None,
                                   'A new beta version of pyTivo Desktop is available',
                                   'Update Available', wx.YES_NO | wx.ICON_INFORMATION | wx.YES_DEFAULT | wx.CENTRE)

            currentText = 'Current version: ' + version[0] + '.' + version[1] + '.' + version[2]
            newText = 'Beta version: ' + newVersion
            extMessageText = currentText + '\n' + newText + '\n\nWould you like to download it now?'
            dlg.SetExtendedMessage(extMessageText)

            doDownload = dlg.ShowModal() == wx.ID_YES
            dlg.Destroy()

            if doDownload:
                if isMacOSX:
                    webbrowser.open('http://www.pytivodesktop.com/mac_beta.html', new=2, autoraise=True)
                elif isWindows:
                    webbrowser.open('http://www.pytivodesktop.com/windows_beta.html', new=2, autoraise=True)
        elif error:
            dlg = wx.MessageDialog(None,
                                   'Error contacting server, try again later',
                                   'Error', wx.OK | wx.ICON_ERROR | wx.CENTRE)

            dlg.ShowModal()
            dlg.Destroy()
        else:
            dlg = wx.MessageDialog(None,
                                   'Your version of pyTivo Desktop is current',
                                   'No Beta Available', wx.OK | wx.ICON_INFORMATION | wx.CENTRE)

            dlg.ShowModal()
            dlg.Destroy()

    def CheckBetaVersionFunc(self, silentCheck):
        isError = False
        try:
            if isMacOSX:
                response = UrlRead('http://www.pytivodesktop.com/mac/beta.info')
            elif isWindows:
                response = UrlRead('http://www.pytivodesktop.com/win32/beta.info')
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
                wx.CallAfter(self.PromptBetaUpdate, True, newVersion, False)
            elif not silentCheck:
                wx.CallAfter(self.PromptBetaUpdate, False, '', False)

        except (OSError, ValueError, IndexError):
            isError = True

        if isError:
            if not silentCheck:
                wx.CallAfter(self.PromptBetaUpdate, False, '', True)


    def CheckVersionFunc(self, silentCheck):
        isError = False
        try:
            if isMacOSX:
                versionUrl = 'http://www.pytivodesktop.com/php/update_check.php?platform=mac&current=' + versionString
                response = UrlRead(versionUrl)
            elif isWindows:
                versionUrl = 'http://www.pytivodesktop.com/php/update_check.php?platform=win&current=' + versionString
                response = UrlRead(versionUrl)
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

            ignoreVersion = GetUpdateIgnoreVersion()
            if int(latest[0]) <= int(ignoreVersion[0]):
                if int(latest[1]) <= int(ignoreVersion[1]):
                    if int(latest[2]) <= int(ignoreVersion[2]):
                        newer = False

            if newer:
                newVersion = latest[0] + '.' + latest[1] + '.' + latest[2]
                wx.CallAfter(self.PromptVersionUpdate, True, newVersion, False)
            else:
                if IsBetaTester():
                    self.CheckBetaVersionFunc(silentCheck)
                else:
                    if not silentCheck:
                        wx.CallAfter(self.PromptVersionUpdate, False, '', False)

        except (OSError, ValueError, IndexError):
            isError = True

        if isError:
            if not silentCheck:
                wx.CallAfter(self.PromptVersionUpdate, False, '', True)

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
            self.versionCheckTimer.daemon = True
            self.versionCheckTimer.start()


    def StartPyTivoThread(self):
        if isWindows:
            if self.isPyTivoService:
                self.StartPyTivoService()
                return

        if self.isPyTivoRunning or self.pyTivoThread != None:
            return

        self.pyTivoStop = threading.Event()
        self.pyTivoThread = threading.Thread(target=self.pyTivoThreadFunc, daemon=True)
        self.pyTivoThread.start()


    def StartPyTivoService(self):
        if isWindows:
            if self.isPyTivoService:
                try:
                    status = win32serviceutil.QueryServiceStatus('pyTivo')
                    if status[1] == win32service.SERVICE_RUNNING:
                        return

                    win32serviceutil.StartService('pyTivo')
                except Exception as msg:
                    pass


    def StartPyTivoServiceMonitorThread(self):
        self.pyTivoServiceMonitorStop = threading.Event()
        self.pyTivoServiceMonitorThread = threading.Thread(target=self.pyTivoServiceMonitorThreadFunc, daemon=True)
        self.pyTivoServiceMonitorThread.start()


    def ConfirmPyTivoStop(self):
        uploadCount = GetUploadQueueCount()
        downloadCount = GetDownloadQueueCount()
        message = ''
        if downloadCount > 0 and uploadCount == 0:
            message = 'There are still %d recordings being downloaded, are you sure you want to stop pyTivo?' % downloadCount
        elif uploadCount > 0 and downloadCount == 0:
            message = 'There are still %d videos being uploaded, are you sure you want to stop pyTivo?' % uploadCount
        elif downloadCount > 0 and uploadCount > 0:
            message = 'There are still %d recordings being downloaded and %d videos being uploaded, are you sure you want to stop pyTivo?' % (
            downloadCount, uploadCount)

        if downloadCount > 0 or uploadCount > 0:
            dlg = wx.MessageDialog(None, message, 'Confirm',
                                   wx.YES_NO | wx.ICON_EXCLAMATION | wx.NO_DEFAULT | wx.CENTRE)

            stopPyTivo = dlg.ShowModal() == wx.ID_YES
            dlg.Destroy()
            return stopPyTivo

        return True


    def StopPyTivoThread(self):
        if self.isPyTivoRunning:
            if not self.ConfirmPyTivoStop():
                return False
            else:
                CancelAllTransfers()

            self.pyTivoStop.set()
            self.pyTivoThread.join()
            return True

        return True


    def StopPyTivoService(self):
        if not isWindows:
            return False

        try:
            status = win32serviceutil.QueryServiceStatus('pyTivo')
            if status[1] != win32service.SERVICE_RUNNING:
                return True
        except Exception:
            return False

        if not self.ConfirmPyTivoStop():
            return False
        else:
            CancelAllTransfers()

        try:
            win32serviceutil.StopService('pyTivo')
        except Exception:
            return False

        return True


    def RestartPyTivo(self):
        if isWindows and self.isPyTivoService:
            try:
                win32serviceutil.RestartService('pyTivo')
            except Exception:
                pass
            else:
                return

        try:
            LocalCommand('Restart', 'Settings')
        except OSError:
            pass


    def pyTivoThreadFunc(self):
        if 'APPDATA' in os.environ:
            appdataDir = os.path.join(os.environ['APPDATA'], 'pyTivo')
            if not os.path.exists(appdataDir):
                os.makedirs(appdataDir)

            logPath = os.path.join(appdataDir, 'log.txt')
        else:
            logPath = os.path.join(AppDir(), 'log.txt')

        logFile = None
        if getattr(sys, 'frozen', False):
            logFile = open(logPath, 'w', encoding='utf-8')
            pyTivoProcess = subprocess.Popen([os.path.join(os.path.dirname(sys.executable), 'pyTivo')],
                                             stdout=logFile, stderr=subprocess.STDOUT, stdin=subprocess.DEVNULL)
        else:
            pyTivoScript = os.path.join(os.path.dirname(os.path.dirname(os.path.abspath(__file__))), 'pyTivo.py')
            pyTivoProcess = subprocess.Popen([sys.executable, pyTivoScript])

        self.isPyTivoRunning = True
        self.UpdateIcon()

        while pyTivoProcess.poll() is None:
            time.sleep(1.0)

            if self.pyTivoStop.is_set():
                try:
                    LocalCommand('Quit', 'Settings')
                    pyTivoProcess.wait(timeout=30)
                except (OSError, subprocess.TimeoutExpired):
                    pyTivoProcess.terminate()
                break

        if logFile is not None:
            logFile.close()

        self.isPyTivoRunning = False
        self.pyTivoThread = None
        self.UpdateIcon()


    def pyTivoServiceMonitorThreadFunc(self):
        if isWindows:
            while True:
                if self.pyTivoServiceMonitorStop.is_set():
                    break

                try:
                    status = win32serviceutil.QueryServiceStatus('pyTivo')
                    self.isPyTivoRunning = status[1] == win32service.SERVICE_RUNNING
                    self.UpdateIcon()
                except Exception:
                    break

                time.sleep(2.0)


    def OnOpenDesktop(self, event):
        self.OpenDesktop()


    def OnStartPyTivoThread(self, event):
        self.StartPyTivoThread()


    def OnStopPyTivoThread(self, event):
        if isWindows:
            if self.isPyTivoService:
                self.StopPyTivoService()
                return

        self.StopPyTivoThread()


    def OnRestartPyTivo(self, event):
        self.RestartPyTivo()


    def OnCheckVersion(self, event):
        self.CheckVersion(False)


    def OnExit(self, event):
        if self.isPyTivoRunning:
            if self.isPyTivoService:
                # Ask user if they want to keep the pyTivo serivce running
                dlg = wx.MessageDialog(None, 'Do you want to keep the pyTivo service running?', 'Confirm',
                                       wx.YES_NO | wx.ICON_EXCLAMATION | wx.NO_DEFAULT | wx.CENTRE)
                stopPyTivo = dlg.ShowModal() == wx.ID_NO
                dlg.Destroy()
                if stopPyTivo:
                    self.StopPyTivoService()

                self.pyTivoServiceMonitorStop.set()
                self.pyTivoServiceMonitorThread.join()
            else:
                # Confirm user wants to exit
                dlg = wx.MessageDialog(None, 'Are you sure you want to exit pyTivo?', 'Confirm',
                                       wx.YES_NO | wx.ICON_EXCLAMATION | wx.NO_DEFAULT | wx.CENTRE)
                exitPyTivo = dlg.ShowModal() == wx.ID_YES
                dlg.Destroy()
                if not exitPyTivo:
                    return

                if not self.StopPyTivoThread():
                    return

        # Kill version check timer
        if self.versionCheckTimer != None:
            if self.versionCheckTimer.is_alive():
                self.versionCheckTimer.cancel()
                self.versionCheckTimer = None

        wx.CallAfter(self.Destroy)
        self.frame.Close()


    def __del__(self):
        if isWindows and getattr(self, 'mutex', None):
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

    main()