import json
import os
import sys

from plugin import Plugin

try:
    from videoredo import interface
except ImportError:     # VideoReDo needs Windows and comtypes
    interface = None

# determine if application is a script file or frozen exe
SCRIPTDIR = os.path.dirname(__file__)
if getattr(sys, 'frozen', False):
    SCRIPTDIR = os.path.join(sys._MEIPASS, 'plugins', 'vrd')

CLASS_NAME = 'VRD'

class VRD(Plugin):
    CONTENT_TYPE = 'text/html'

    def GetVersion(self, handler, query):
        json_config = {'version': '', 'isPro': False}
        if interface is None:
            handler.send_json(json.dumps(json_config))
            return
        vrd = interface.VideoReDo()
        json_config['version'] = vrd.get_version()
        json_config['isPro'] = vrd.get_is_pro()
        handler.send_json(json.dumps(json_config))


    def GetProfileList(self, handler, query):
        profiles = interface.VideoReDo().get_profiles() if interface else {}
        handler.send_json(json.dumps(profiles))


    def GetAdScanProfileList(self, handler, query):
        profiles = (interface.VideoReDo().get_adscan_profiles()
                    if interface else {})
        handler.send_json(json.dumps(profiles))

