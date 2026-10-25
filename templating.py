"""Render pyTivo's Jinja2 templates.

Templates are addressed by absolute path, compiled once, and reloaded
automatically if the file changes on disk. Whitespace handling mirrors
the old Cheetah templates: a line holding only a {% tag %} produces no
output. None renders as an empty string, as it did under Cheetah.
"""
import os

import jinja2


def _load(path):
    with open(path, encoding='utf-8') as f:
        source = f.read()
    mtime = os.path.getmtime(path)
    return source, path, lambda: os.path.getmtime(path) == mtime


_env = jinja2.Environment(
    loader=jinja2.FunctionLoader(_load),
    autoescape=False,           # the templates escape explicitly
    trim_blocks=True,
    lstrip_blocks=True,
    keep_trailing_newline=True,
    finalize=lambda value: '' if value is None else value,
)


def render(path, **context):
    """Render the template file at path with the given variables."""
    return _env.get_template(os.path.abspath(path)).render(**context)
