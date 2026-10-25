"""A small, thread-safe LRU (Least-Recently-Used) cache.

Behaves like a dict whose least-recently-used entries are discarded once
more than ``size`` items are stored. Each entry also records when it was
last written, available through ``mtime(key)``, so callers can decide
whether a cached value has gone stale.

Replaces the original heap-based lrucache 0.2 by Evan Prodromou, keeping
the same public interface.
"""

import threading
import time
from collections import OrderedDict

__all__ = ['CacheKeyError', 'LRUCache', 'DEFAULT_SIZE']

DEFAULT_SIZE = 16


class CacheKeyError(KeyError):
    """Raised when a cache record is accessed which no longer exists (or
    never did)."""


class LRUCache:
    def __init__(self, size=DEFAULT_SIZE):
        if not isinstance(size, int):
            raise TypeError(size)
        if size <= 0:
            raise ValueError(size)
        self._data = OrderedDict()   # key -> (obj, mtime)
        self._lock = threading.RLock()
        self._size = size

    @property
    def size(self):
        return self._size

    @size.setter
    def size(self, value):
        with self._lock:
            self._size = value
            self._shrink()

    def _shrink(self):
        while len(self._data) > self._size:
            self._data.popitem(last=False)

    def __len__(self):
        return len(self._data)

    def __contains__(self, key):
        return key in self._data

    def __setitem__(self, key, obj):
        with self._lock:
            self._data[key] = (obj, time.time())
            self._data.move_to_end(key)
            self._shrink()

    def __getitem__(self, key):
        with self._lock:
            try:
                obj, _ = self._data[key]
            except KeyError:
                raise CacheKeyError(key) from None
            self._data.move_to_end(key)
            return obj

    def __delitem__(self, key):
        with self._lock:
            try:
                del self._data[key]
            except KeyError:
                raise CacheKeyError(key) from None

    def __iter__(self):
        # Iterate over a snapshot, least recently used first, so callers
        # may delete entries while looping.
        with self._lock:
            keys = list(self._data)
        return iter(keys)

    def get(self, key, default=None):
        try:
            return self[key]
        except CacheKeyError:
            return default

    def clear(self):
        with self._lock:
            self._data.clear()

    def mtime(self, key):
        """Return the last modification time for the cache record with key."""
        try:
            return self._data[key][1]
        except KeyError:
            raise CacheKeyError(key) from None

    def __repr__(self):
        return '<%s (%d elements)>' % (self.__class__.__name__, len(self))
