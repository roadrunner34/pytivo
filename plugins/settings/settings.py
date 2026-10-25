import json
import logging
import os
import shutil
import stat
import subprocess
import sys
from urllib.parse import quote, unquote

import config
from plugin import Plugin
from templating import render
from plugins.settings import buildhelp

logger = logging.getLogger('pyTivo.settings')

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

SETTINGS_TEMPLATE = os.path.join(SCRIPTDIR, 'templates', 'settings.tmpl')

BOOLEAN_SETTINGS = {
    'Server': {'debug', 'nosettings', 'togo_save_txt', 'togo_decode',
               'togo_sortable_names', 'tivolibre_upload', 'beta_tester',
               'vrd_prompt', 'vrd_decrypt_qsf', 'vrd_delete_on_success',
               'free_space_check', 'togo_slow_on_retry'},
    'TiVos': {'optres'},
    'Shares': {'force_alpha', 'force_ffmpeg'},
}

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
        logger.info('pyTivo has been soft reset.')

    def Settings(self, handler, query):
        # Read config file new each time in case there was any outside edits
        config.reset()

        shares_data = []
        for section in config.config.sections():
            if not section.startswith(('_tivo_', 'Server')):
                if (not (config.config.has_option(section, 'type')) or config.config.get(section, 'type').lower() not in ['settings', 'togo']):
                    shares_data.append((section, dict(config.config.items(section, raw=True))))

        def section_data(section):
            return dict(config.config.items(section, raw=True))

        tivos_data = [(section, section_data(section))
                      for section in config.config.sections()
                      if section.startswith('_tivo_')
                      and not section.startswith(('_tivo_SD', '_tivo_HD',
                                                  '_tivo_4K'))]
        handler.send_html(render(
            SETTINGS_TEMPLATE,
            mode=buildhelp.mode,
            options=buildhelp.options,
            container=handler.cname,
            quote=quote,
            server_data=section_data('Server'),
            server_known=buildhelp.getknown('server'),
            fk_tivos_data=section_data('_tivo_4K'),
            fk_tivos_known=buildhelp.getknown('fk_tivos'),
            hd_tivos_data=section_data('_tivo_HD'),
            hd_tivos_known=buildhelp.getknown('hd_tivos'),
            sd_tivos_data=section_data('_tivo_SD'),
            sd_tivos_known=buildhelp.getknown('sd_tivos'),
            shares_data=shares_data,
            shares_known=buildhelp.getknown('shares'),
            tivos_data=tivos_data,
            tivos_known=buildhelp.getknown('tivos'),
            help_list=buildhelp.gethelp(),
            has_shutdown=hasattr(handler.server, 'shutdown')))

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

        new_section = query.get('new_Section', [' '])[0]
        if new_section != ' ' and not config.config.has_section(new_section):
            config.config.add_section(new_section)
        config.write()

        if getattr(sys, 'frozen', False):
            if (sys.platform == "win32" and
                config.config.has_option('Server', 'tivo_mak')):
                tivomak_path = os.path.join(os.path.dirname(sys.executable), 'dshow', 'tivomak')
                tmakcmd = [tivomak_path, '-set', config.config.get('Server', 'tivo_mak')]
                try:
                    subprocess.Popen(tmakcmd)
                except OSError as msg:
                    logger.error('Unable to run tivomak: %s', msg)

        handler.redir(SETTINGS_MSG, 5)

    def GetSettings(self, handler, query):
        # Read config file new each time in case there was any outside edits
        config.reset()

        shares_data = []
        for section in config.config.sections():
            if not section.startswith(('_tivo_', 'Server')):
                if (not (config.config.has_option(section, 'type')) or config.config.get(section, 'type').lower() not in ['settings', 'togo']):
                    shares_data.append((section, dict(config.config.items(section, raw=True))))

        def section_dict(section, booleans):
            result = {}
            for name, value in config.config.items(section):
                if name in booleans:
                    try:
                        value = config.config.getboolean(section, name)
                    except ValueError:
                        pass
                result[name] = value
            return result

        json_config = {'Server': {}, 'TiVos': {}, 'Shares': {}}
        for section in config.config.sections():
            if section == 'Server':
                json_config['Server'] = section_dict(
                    section, BOOLEAN_SETTINGS['Server'])
            elif section.startswith('_tivo_'):
                json_config['TiVos'][section] = section_dict(
                    section, BOOLEAN_SETTINGS['TiVos'])
            elif (not config.config.has_option(section, 'type') or
                  config.config.get(section, 'type').lower()
                  not in ['settings', 'togo']):
                json_config['Shares'][section] = section_dict(
                    section, BOOLEAN_SETTINGS['Shares'])

        handler.send_json(json.dumps(json_config))

    def GetOSName(self, handler, query):
        json_config = {}
        json_config['os'] = sys.platform
        handler.send_json(json.dumps(json_config))

    def GetDriveList(self, handler, query):
        json_config = {}
        try:
            import psutil
        except ImportError:
            psutil = None
            if sys.platform != 'darwin':
                logger.error('GetDriveList requires the psutil package')

        if sys.platform == 'win32' and psutil:
            for index, part in enumerate(psutil.disk_partitions(all=True)):
                if part.fstype == '':
                    continue

                if 'dontbrowse' in part.opts:
                    continue

                json_config[index] = {}
                json_config[index]['mountpoint'] = part.mountpoint
                json_config[index]['name'] = (volume_label(part.device) or
                                              'Local Disk')
        elif sys.platform == 'darwin':
            for index, fname in enumerate(os.listdir('/Volumes')):
                json_config[index] = {}
                json_config[index]['mountpoint'] = '/Volumes/' + fname
                json_config[index]['name'] = fname
        elif sys.platform.startswith('linux') and psutil:
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
            path = unquote(query['Path'][0])

            try:
                stats = shutil.disk_usage(path)
                json_config['total'] = stats.total
                json_config['free'] = stats.free
                json_config['used'] = stats.used
            except OSError:
                pass

        handler.send_json(json.dumps(json_config))

    def has_hidden_attribute(self, filepath):
        result = False
        if sys.platform == 'win32':
            try:
                attrs = os.stat(filepath).st_file_attributes
                result = bool(attrs & stat.FILE_ATTRIBUTE_HIDDEN)
            except (OSError, AttributeError):
                result = False
        else:
            if '/.' in filepath:
                result = True

        return result

    def GetFileList(self, handler, query):
        json_config = {}

        basepath = '/'
        if 'BasePath' in query:
            basepath = unquote(query['BasePath'][0])

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
        except OSError as msg:
            logger.error('Unable to list %s: %s', basepath, msg)

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

        try:
            with open(logPath, encoding='utf-8', errors='replace') as logFile:
                handler.send_html(logFile.read())
        except OSError:
            handler.send_error(404)


def volume_label(device):
    """Return the volume label for a Windows drive, or ''."""
    import ctypes
    buf = ctypes.create_unicode_buffer(261)
    if ctypes.windll.kernel32.GetVolumeInformationW(
            ctypes.c_wchar_p(device), buf, len(buf),
            None, None, None, None, 0):
        return buf.value
    return ''

