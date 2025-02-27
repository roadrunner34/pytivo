#!/usr/bin/env python
# $Id: Parser.py,v 1.71 2007/10/02 01:36:26 tavis_rudd Exp $
"""Parser classes for Cheetah's Compiler

Classes:
  ParseError( Exception )
  _LowLevelParser( Cheetah.SourceReader.SourceReader ), basically a lexer
  _HighLevelParser( _LowLevelParser )
  Parser === _HighLevelParser (an alias)
  
Meta-Data
================================================================================
Author: Tavis Rudd <tavis@damnsimple.com>
License: This software is released for unlimited distribution under the
         terms of the MIT license.  See the LICENSE file.
Version: $Revision: 1.71 $
Start Date: 2001/08/01
Last Revision Date: $Date: 2007/10/02 01:36:26 $
"""
__author__ = "Tavis Rudd <tavis@damnsimple.com>"
__revision__ = "$Revision: 1.71 $"[11:-2]

import re
import sys
import os
import os.path
from time import time as currentTime
import warnings
import traceback

from Cheetah.SourceReader import SourceReader
from Cheetah.Unspecified import Unspecified
from Cheetah.Macros.I18n import I18n
from Cheetah.Utils.Misc import useOrRaise
from Cheetah.Utils.Indenter import indentize

# Python 3 compatibility
StringType = str
ListType = list
TupleType = tuple
ClassType = type
TypeType = type

# Constants
STATIC_CACHE = 'static'
REFRESH_CACHE = 'refresh'
SET_LOCAL = 'local'
SET_GLOBAL = 'global'
SET_MODULE = 'module'

# Regular expressions
specialVarRE = re.compile(r'([a-zA-Z_][a-zA-Z_0-9]*)')
unicodeDirectiveRE = re.compile(r'#unicode[( ](?:[a-zA-Z_][a-zA-Z_0-9]*)?[) ]')
encodingDirectiveRE = re.compile(r'#encoding[( ](?:[a-zA-Z_][a-zA-Z_0-9]*)?[) ]')
escapedNewlineRE = re.compile(r'\\n')

class ParseError(Exception):
    pass

class _LowLevelParser(SourceReader):
    """A low-level lexer class that provides basic services for parsing Cheetah
    source code. This class is not intended to be used directly, but rather
    through the _HighLevelParser class."""
    pass

class _HighLevelParser(_LowLevelParser):
    """A high-level parser class that provides more advanced parsing services.
    This is the class that actually parses Cheetah source code into a form
    that can be used by the compiler."""
    pass

# Make Parser an alias for _HighLevelParser
Parser = _HighLevelParser
