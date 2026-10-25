import hashlib
import struct

import pytest

import turing


def test_crypt_round_trip():
    key = hashlib.sha1(b'key').digest()
    iv = hashlib.sha1(b'iv').digest()
    data = bytes(range(256)) * 3

    encrypted = turing.Turing(key, iv).crypt(data, 100)
    assert encrypted != data
    assert turing.Turing(key, iv).crypt(encrypted, 100) == data


def test_keystream_is_deterministic_and_skippable():
    key = hashlib.sha1(b'key').digest()
    iv = hashlib.sha1(b'iv').digest()
    full = turing.Turing(key, iv).gen(0, 100)
    assert isinstance(full, bytes) and len(full) == 100
    assert turing.Turing(key, iv).gen(37, 40) == full[37:77]


def test_bad_key_length():
    with pytest.raises(turing.KeyLengthError):
        turing.Turing(b'abc')


def test_tivo_metadata_decryption(tmp_path):
    """Build an encrypted .tivo header the way a TiVo does and read it back
    with the pure-Python tdcat replacement."""
    import metadata

    mak = '1234567890'
    xml_key = b'0123456789abcdef'
    details = (b'<?xml version="1.0" encoding="utf-8"?><TvBusMarshalledStruct>'
               b'<showing><program><title>Caf\xc3\xa9</title></program>'
               b'</showing></TvBusMarshalledStruct>')

    hexmak = hashlib.md5(('tivo:TiVo DVR:' + mak).encode()).hexdigest()
    key = hashlib.sha1(hexmak.encode() + xml_key).digest()[:16] + b'\0' * 4
    turkey = hashlib.sha1(key[:17]).digest()
    turiv = hashlib.sha1(key).digest()

    def chunk(cid, enc, data):
        return struct.pack('>LLHH', len(data) + 12, len(data), cid, enc) + data

    chunk1 = chunk(1, 0, b'unused')
    chunk2_start = 16 + len(chunk1) + 12
    encrypted = turing.Turing(turkey, turiv).crypt(details, chunk2_start)
    body = chunk1 + chunk(2, 1, encrypted) + chunk(3, 0, xml_key)
    header = b'TiVo' + struct.pack('>HHHLH', 4, 13, 0, 16 + len(body), 3)

    path = tmp_path / 'show.tivo'
    path.write_bytes(header + body + b'\0' * 64)

    assert metadata._tdcat_py(str(path), mak) == details
