import logging
import os
import sys
import json
import subprocess
from urllib import quote, unquote

from Cheetah.Template import Template

import buildhelp
import config
from plugin import EncodeUnicode, Plugin

# determine if application is a script file or frozen exe
SCRIPTDIR = os.path.dirname(__file__)
if getattr(sys, 'frozen', False):
    SCRIPTDIR = os.path.join(sys._MEIPASS, 'plugins', 'settings')

CLASS_NAME = 'Settings'

# Some error/status message templates

RESET_MSG = """<h3>Soft Reset</h3> <p>pyTivo has reloaded the 
pyTivo.conf file and all changes should now be in effect.</p>"""

RESTART_MSG = """<h3>Restart</h3> <p>pyTivo will now restart.</p>"""

GOODBYE_MSG = 'Goodbye.\n'

SETTINGS_MSG = """<h3>Settings Saved</h3> <p>Your settings have been 
saved to the pyTivo.conf file. However you may need to do a <b>Soft 
Reset</b> or <b>Restart</b> before these changes will take effect.</p>"""

# Preload the templates
tsname = os.path.join(SCRIPTDIR, 'templates', 'settings.tmpl')
SETTINGS_TEMPLATE = file(tsname, 'rb').read()

class Settings(Plugin):
    CONTENT_TYPE = 'text/html'

    def Quit(self, handler, query):
        if hasattr(handler.server, 'shutdown'):
            handler.send_fixed(GOODBYE_MSG, 'text/plain')
            if handler.server.in_service:
                handler.server.stop = True
            else:
                handler.server.shutdown()
            handler.server.socket.close()
        else:
            handler.send_error(501)

    def Restart(self, handler, query):
        if hasattr(handler.server, 'shutdown'):
            handler.redir(RESTART_MSG, 10)
            handler.server.restart = True
            if handler.server.in_service:
                handler.server.stop = True
            else:
                handler.server.shutdown()
            handler.server.socket.close()
        else:
            handler.send_error(501)

    def Reset(self, handler, query):
        config.reset()
        handler.server.reset()
        handler.redir(RESET_MSG, 3)
        logging.getLogger('pyTivo.settings').info('pyTivo has been soft reset.')

    def Settings(self, handler, query):
        # Read config file new each time in case there was any outside edits
        config.reset()

        shares_data = []
        for section in config.config.sections():
            if not section.startswith(('_tivo_', 'Server')):
                if (not (config.config.has_option(section, 'type')) or config.config.get(section, 'type').lower() not in ['settings', 'togo']):
                    shares_data.append((section, dict(config.config.items(section, raw=True))))

        t = Template(SETTINGS_TEMPLATE, filter=EncodeUnicode)
        t.mode = buildhelp.mode
        t.options = buildhelp.options
        t.container = handler.cname
        t.quote = quote
        t.server_data = dict(config.config.items('Server', raw=True))
        t.server_known = buildhelp.getknown('server')
        t.fk_tivos_data = dict(config.config.items('_tivo_4K', raw=True))
        t.fk_tivos_known = buildhelp.getknown('fk_tivos')
        t.hd_tivos_data = dict(config.config.items('_tivo_HD', raw=True))
        t.hd_tivos_known = buildhelp.getknown('hd_tivos')
        t.sd_tivos_data = dict(config.config.items('_tivo_SD', raw=True))
        t.sd_tivos_known = buildhelp.getknown('sd_tivos')
        t.shares_data = shares_data
        t.shares_known = buildhelp.getknown('shares')
        t.tivos_data = [(section, dict(config.config.items(section, raw=True)))
                        for section in config.config.sections()
                        if section.startswith('_tivo_')
                        and not section.startswith(('_tivo_SD', '_tivo_HD',
                                                    '_tivo_4K'))]
        t.tivos_known = buildhelp.getknown('tivos')
        t.help_list = buildhelp.gethelp()
        t.has_shutdown = hasattr(handler.server, 'shutdown')
        handler.send_html(str(t))

    def each_section(self, query, label, section):
        new_setting = new_value = ' '
        if config.config.has_section(section):
            config.config.remove_section(section)
        config.config.add_section(section)
        for key, value in query.items():
            key = key.replace('opts.', '', 1)
            if key.startswith(label + '.'):
                _, option = key.split('.')
                default = buildhelp.default.get(option, ' ')
                value = value[0]
                if not config.config.has_section(section):
                    config.config.add_section(section)
                if option == 'new__setting':
                    new_setting = value
                elif option == 'new__value':
                    new_value = value
                elif value not in (' ', default):
                    config.config.set(section, option, value)
        if not(new_setting == ' ' and new_value == ' '):
            config.config.set(section, new_setting, new_value)

    def UpdateSettings(self, handler, query):
        config.reset()
        for section in ['Server', '_tivo_SD', '_tivo_HD', '_tivo_4K']:
            self.each_section(query, section, section)

        sections = query['Section_Map'][0].split(']')[:-1]
        for section in sections:
            ID, name = section.split('|')
            if query[ID][0] == 'Delete_Me':
                config.config.remove_section(name)
                continue
            if query[ID][0] != name:
                config.config.remove_section(name)
                config.config.add_section(query[ID][0])
            self.each_section(query, ID, query[ID][0])

        if query['new_Section'][0] != ' ':
            config.config.add_section(query['new_Section'][0])
        config.write()

        if getattr(sys, 'frozen', False):
            if sys.platform == "win32":
                tivomak_path = os.path.join(os.path.dirname(sys.executable), 'dshow', 'tivomak')
                tmakcmd = [tivomak_path, '-set', config.config.get('Server', 'tivo_mak')]
                subprocess.Popen(tmakcmd, shell=True)

        handler.redir(SETTINGS_MSG, 5)

    def GetSettings(self, handler, query):
        # Read config file new each time in case there was any outside edits
        config.reset()

        shares_data = []
        for section in config.config.sections():
            if not section.startswith(('_tivo_', 'Server')):
                if (not (config.config.has_option(section, 'type')) or config.config.get(section, 'type').lower() not in ['settings', 'togo']):
                    shares_data.append((section, dict(config.config.items(section, raw=True))))

        json_config = {}
        json_config['Server'] = {}
        json_config['TiVos'] = {}
        json_config['Shares'] = {}
        for section in config.config.sections():
            if section == 'Server':
                for name, value in config.config.items(section):
                    if name in {'debug', 'nosettings', 'togo_save_txt', 'togo_decode', 'togo_sortable_names', 'tivolibre_upload', 'beta_tester', 'vrd_prompt', 'vrd_decrypt_qsf', 'vrd_delete_on_success'}:
                        try:
                            json_config['Server'][name] = config.config.getboolean(section, name)
                        except ValueError:
                            json_config['Server'][name] = value
                    else:
                        json_config['Server'][name] = value
            else:
                if section.startswith('_tivo_'):
                    json_config['TiVos'][section] = {}
                    for name, value in config.config.items(section):
                        if name in {'optres'}:
                            try:
                                json_config['TiVos'][section][name] = config.config.getboolean(section, name)
                            except ValueError:
                                json_config['TiVos'][section][name] = value
                        else:
                            json_config['TiVos'][section][name] = value
                else:
                    if (not (config.config.has_option(section, 'type')) or config.config.get(section, 'type').lower() not in ['settings', 'togo']):
                        json_config['Shares'][section] = {}
                        for name, value in config.config.items(section):
                            if name in {'force_alpha', 'force_ffmpeg'}:
                                try:
                                    json_config['Shares'][section][name] = config.config.getboolean(section, name)
                                except ValueError:
                                    json_config['Shares'][section][name] = value
                            else:
                                json_config['Shares'][section][name] = value

        handler.send_json(json.dumps(json_config))

    def GetOSName(self, handler, query):
        json_config = {}
        json_config['os'] = sys.platform
        handler.send_json(json.dumps(json_config))

    def GetDriveList(self, handler, query):
        import psutil
        json_config = {}
        if sys.platform == 'win32':
            import win32api
            for index, part in enumerate(psutil.disk_partitions(all=True)):
                if part.fstype == '':
                    continue

                if 'dontbrowse' in part.opts:
                    continue

                json_config[index] = {}
                json_config[index]['mountpoint'] = part.mountpoint

                info = win32api.GetVolumeInformation(part.device)
                if (info[0] == ''):
                    json_config[index]['name'] = 'Local Disk'
                else:
                    json_config[index]['name'] = info[0]
        elif sys.platform == 'darwin':
            for index, fname in enumerate(os.listdir('/Volumes')):
                json_config[index] = {}
                json_config[index]['mountpoint'] = '/Volumes/' + fname
                json_config[index]['name'] = fname
        elif sys.platform == 'linux2':
            print psutil.disk_partitions(all=True)
            for index, part in enumerate(psutil.disk_partitions(all=True)):
                if not part.fstype in ['msdos', 'ntfs', 'ext2', 'ext3', 'ext4']:
                    continue

                json_config[index] = {}
                json_config[index]['mountpoint'] = part.mountpoint
                json_config[index]['name'] = part.device


        handler.send_json(json.dumps(json_config))

    def GetDiskUsage(self, handler, query):
        json_config = {}
        json_config['total'] = 9999999999999999999
        json_config['free'] = 9999999999999999999
        json_config['used'] = 0

        if 'Path' in query:
            path = unicode(unquote(query['Path'][0]), 'utf-8')

            try:
                if sys.platform == 'win32':
                    import psutil
                    stats = psutil.disk_usage(path)
                    json_config['total'] = stats.total
                    json_config['free'] = stats.free
                    json_config['used'] = stats.used
                else:
                    stats = os.statvfs(path)
                    json_config['total'] = stats.f_blocks * stats.f_frsize
                    json_config['free'] = stats.f_bavail * stats.f_frsize
                    json_config['used'] = (stats.f_blocks - stats.f_bfree) * stats.f_frsize
            except:
                pass

        handler.send_json(json.dumps(json_config))

    def has_hidden_attribute(self, filepath):
        result = False
        if sys.platform == 'win32':
            import ctypes
            try:
                attrs = ctypes.windll.kernel32.GetFileAttributesW(unicode(filepath))
                if attrs != -1:
                    result = bool(attrs & 2)
            except (AttributeError, AssertionError):
                result = False
        else:
            if '/.' in filepath:
                result = True

        return result

    def GetFileList(self, handler, query):
        json_config = {}

        basepath = '/'
        if 'BasePath' in query:
            basepath = unicode(unquote(query['BasePath'][0]), 'utf-8')

        try:
            for index, fname in enumerate(os.listdir(basepath)):
                path = os.path.join(basepath, fname)
                if self.has_hidden_attribute(path):
                    continue

                json_config[index] = {}
                json_config[index]['path'] = path
                if os.path.isdir(path):
                    if sys.platform == 'darwin':
                        if path.endswith('.app'):
                            json_config[index]['isFolder'] = False
                        else:
                            json_config[index]['isFolder'] = True
                    else:
                        json_config[index]['isFolder'] = True
                else:
                    json_config[index]['isFolder'] = False
        except:
            print "Error"

        handler.send_json(json.dumps(json_config))


    def GetLogText(self, handler, query):
        if config.isRunningInService():
            programdataDir = os.path.join(os.environ['ALLUSERSPROFILE'], 'pyTivo')
            if not os.path.exists(programdataDir):
                os.makedirs(programdataDir)

            logPath = os.path.join(programdataDir, 'log.txt')
        else:
            if 'APPDATA' in os.environ:
                appdataDir = os.path.join(os.environ['APPDATA'], 'pyTivo')
                if not os.path.exists(appdataDir):
                    os.makedirs(appdataDir)

                logPath = os.path.join(appdataDir, 'log.txt')
            else:
                if sys.platform == 'darwin':
                    logDir = os.path.dirname(os.path.dirname(
                        os.path.dirname(os.path.dirname(sys.executable))))  # pyTivo tray is inside a .app bundle
                else:
                    logDir = os.path.dirname(sys.executable)

                logPath = os.path.join(logDir, 'log.txt')

        with open(logPath, 'r') as logFile:
            handler.send_html(logFile.read())

