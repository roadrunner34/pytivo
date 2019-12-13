import os
import sys
import struct
import config
from urllib import unquote

import comtypes
import comtypes.client

SCRIPTDIR = os.path.dirname(__file__)
if getattr(sys, 'frozen', False):
    SCRIPTDIR = os.path.join(sys._MEIPASS, 'videoredo')

class VideoReDo():
    def __init__(self):
        comtypes.CoInitialize()

        self.vrd = None
        self.vrd_version = ''
        self.is_v5 = False
        self.output_file = ''

        try:
            vrd_silent = comtypes.client.CreateObject('VideoReDo6.VideoReDoSilent') # check for v6 first
            if not vrd_silent:
                vrd_silent = comtypes.client.CreateObject('VideoReDo5.VideoReDoSilent')
                self.is_v5 = True

            if vrd_silent:
                self.vrd = vrd_silent.VRDInterface
                self.vrd_version = self.vrd.ProgramGetVersionNumber
        except:
            pass


    def __del__(self):
        try:
            if self.vrd:
                self.vrd.ProgramExit()
        except:
            pass

        comtypes.CoUninitialize()


    def get_version(self):
        return self.vrd_version


    def get_profiles(self):
        profiles = {}

        try:
            if self.vrd:
                count = self.vrd.ProfilesGetCount
                if count > 0:
                    for i in range(count):
                        profile_name = self.vrd.ProfilesGetProfileName(i)
                        profiles[profile_name] = {}

                        if self.is_v5:
                            profiles[profile_name]['enabled'] = self.vrd.ProfilesGetProfileEnabled(i)
                        else:
                            profiles[profile_name]['enabled'] = self.vrd.ProfilesGetProfileIsEnabled(i)

                        profiles[profile_name]['extension'] = self.vrd.ProfilesGetProfileExtension(i)
        except:
            pass

        return profiles


    def ad_scan(self, in_file):
        if os.path.isfile(in_file):
            profile_path = os.path.join(SCRIPTDIR, 'profiles', 'AdScan.OP.xml')
            if os.path.isfile(profile_path):
                out_file = self.__get_out_file(in_file, 'vprj', ' (AdScan)')

                try:
                    if self.vrd:
                        if self.vrd.FileOpen(in_file, False):
                            if self.vrd.FileSaveAs(out_file, profile_path):
                                return True
                except:
                    pass

        return False


    def close_file(self):
        try:
            if self.vrd:
                self.vrd.FileClose()
        except:
            pass


    def save_to_profile(self, in_file, save_profile, output_folder=''):
        if os.path.isfile(in_file):
            try:
                if self.vrd:
                    count = self.vrd.ProfilesGetCount
                    out_file = ''
                    profile_found = False
                    if count > 0:
                        for i in range(count):
                            profile_name = self.vrd.ProfilesGetProfileName(i)
                            if save_profile == profile_name:
                                profile_found = True
                                ext = self.vrd.ProfilesGetProfileExtension(i)
                                out_file = self.__get_out_file(in_file, ext, ' (VRD)', output_folder)
                                profile_found = True
                                break

                        if profile_found:
                            if self.vrd.FileOpen(in_file, False):
                                if self.vrd.FileSaveAs(out_file, save_profile):
                                    return True
            except:
                pass

        return False


    def quick_stream_fix(self, in_file, decrypt=False, output_folder=''):
        if os.path.isfile(in_file):
            is_tivo_file = False
            is_ts_file = False
            try:
                f = open(in_file, 'rb')
                header = bytearray(f.read(16))
                if header[0:4].decode("utf-8") == 'TiVo':
                    is_tivo_file = True
                    try:
                        if (header[7] & 0x20 != 0):
                            is_ts_file = True
                    except:
                        pass
                else:
                    try:
                        if struct.unpack_from('>B', header, 0)[0] != 0x47:
                            is_ts_file = True
                    except:
                        pass

                f.close()

                # Choose best profile for QSF
                profile_file = 'ProgramStream.OP.xml'
                if is_tivo_file and not decrypt:
                    profile_file = 'TiVoPS.OP.xml'
                    if is_ts_file:
                        profile_file = 'TiVoTS.OP.xml'
                elif is_ts_file:
                    profile_file = 'TransportStream.OP.xml'

                profile_path = os.path.join(SCRIPTDIR, 'profiles', profile_file)

                if os.path.isfile(profile_path):
                    # Generate output file name
                    ext = 'mpg'
                    if is_tivo_file and not decrypt:
                        ext = 'tivo'
                    else:
                        if is_ts_file:
                            ext = 'ts'

                    out_file = self.__get_out_file(in_file, ext, ' (QSF)', output_folder)

                    if self.vrd:
                        if self.vrd.FileOpen(in_file, True):
                            if self.vrd.FileSaveAs(out_file, profile_path):
                                return True
            except:
                pass

        return False


    def get_status(self):
        status = {}
        try:
            if self.vrd:
                state = self.vrd.OutputGetState
                if state == 1:
                    status['state'] = 'running'
                elif state == 2:
                    status['state'] = 'paused'
                else:
                    status['state'] = 'none'

                status['percent'] = self.vrd.OutputGetPercentComplete
                status['text'] = self.vrd.OutputGetStatusText
        except:
            pass

        return status


    def abort_output(self):
        try:
            if self.vrd:
                if self.vrd.OutputGetState != 0:
                    self.vrd.OutputAbort()
        except:
            pass


    def pause_output(self, pause):
        try:
            if self.vrd:
                if pause:
                    if self.vrd.OutputGetState == 1:
                        self.vrd.OutputPause(True)
                else:
                    if self.vrd.OutputGetState == 2:
                        self.vrd.OutputPause(False)
        except:
            pass


    def __get_out_file(self, in_file, ext, add_on, output_folder=''):
        count = 1
        if not os.path.exists(output_folder):
            output_folder = os.path.dirname(in_file)

        while True:
            out_name = unquote(in_file).split('\\')[-1].split('.')
            out_name.insert(-1, add_on)

            if count > 1:
                out_name.insert(-1, ' (%d)' % count)

            out_name[-1] = ext
            out_name.insert(-1, '.')
            out_name = ''.join(out_name)
            out_file = os.path.join(output_folder, out_name)

            if os.path.isfile(out_file):
                count += 1
                continue

            self.output_file = out_file
            return out_file