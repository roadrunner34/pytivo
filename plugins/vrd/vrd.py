import os
import sys
import json
import struct
import threading
import time
from urllib import quote, unquote

import config
from plugin import EncodeUnicode, Plugin

from videoredo import interface

# determine if application is a script file or frozen exe
SCRIPTDIR = os.path.dirname(__file__)
if getattr(sys, 'frozen', False):
    SCRIPTDIR = os.path.join(sys._MEIPASS, 'plugins', 'vrd')

CLASS_NAME = 'VRD'

class VRD(Plugin):
    CONTENT_TYPE = 'text/html'

    def GetVersion(self, handler, query):
        json_config = {}
        vrd = interface.VideoReDo()
        json_config['version'] = vrd.get_version()
        handler.send_json(json.dumps(json_config))


    def GetProfileList(self, handler, query):
        vrd = interface.VideoReDo()
        profiles = vrd.get_profiles()
        handler.send_json(json.dumps(profiles))

