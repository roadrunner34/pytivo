#!/usr/bin/env python

"""Provides a function that converts a Cheetah template path to a Python module name."""

import string

validChars = string.ascii_letters + string.digits + '_'
validFirstChars = string.ascii_letters + '_'

def convertTmplPathToModuleName(path):
    """Converts a Cheetah template path to a valid Python module name."""
    
    name = path.replace('/', '_').replace('\\', '_').replace('.', '_').replace('-', '_')
    # ensure that all chars are valid
    validatedName = []
    for i, c in enumerate(name):
        if i == 0 and c not in validFirstChars:
            validatedName.append('_')
        elif c not in validChars:
            validatedName.append('_')
        else:
            validatedName.append(c)
    return ''.join(validatedName)
