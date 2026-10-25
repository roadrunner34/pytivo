import time

import pytest

from lrucache import CacheKeyError, LRUCache


def test_evicts_least_recently_used():
    cache = LRUCache(2)
    cache['a'] = 1
    cache['b'] = 2
    cache['a']              # touch 'a' so 'b' becomes the oldest
    cache['c'] = 3
    assert 'a' in cache and 'c' in cache
    assert 'b' not in cache


def test_resize_shrinks():
    cache = LRUCache(5)
    for i in range(5):
        cache[i] = i
    cache.size = 2
    assert list(cache) == [3, 4]


def test_delete_while_iterating():
    cache = LRUCache(5)
    for i in range(5):
        cache[i] = i
    for key in cache:
        del cache[key]
    assert len(cache) == 0


def test_mtime_tracks_writes_not_reads():
    cache = LRUCache(2)
    cache['a'] = 1
    written = cache.mtime('a')
    time.sleep(0.01)
    cache['a']
    assert cache.mtime('a') == written


def test_missing_keys():
    cache = LRUCache(2)
    with pytest.raises(CacheKeyError):
        cache['nope']
    with pytest.raises(KeyError):
        del cache['nope']
    assert cache.get('nope', 7) == 7
