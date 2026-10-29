#!/usr/bin/env python3
# encoding: utf-8
"""生成 115 上传加密层的测试向量，供 Go 单元测试交叉验证。

算法来源：p115client/modules/p115cipher（GPLv3，仅作协议参考，本脚本为独立复算）。
所有常量都是 115 客户端内的固定值，不存在真正的密钥协商。
"""

import base64
import os
import json
import struct
import sys
import zlib
from binascii import b2a_hex
from hashlib import md5, sha1
from urllib.parse import quote_plus

CRC_SALT = b"^j>WD3Kr?J2gLFjD4W2y@"
MD5_SALT = b"Qclm8MGWUv59TnrR0XPg"
AES_PUBKEY = bytes([
    0x1d, 0x03, 0x0e, 0x80, 0xa1, 0x78, 0xdc, 0xee, 0xce, 0xcd,
    0xa3, 0x77, 0xde, 0x12, 0x8d, 0x8e, 0xd9, 0xdd, 0xcf, 0x55,
    0xae, 0x61, 0xed, 0x46, 0xea, 0x12, 0x1a, 0x1c, 0xfc, 0x81,
])


def ecdh_encode_token(ts: int) -> str:
    token = bytearray()
    token += AES_PUBKEY[:15]
    token += b"\x00s\x00\x00\x00"
    token += struct.pack("<I", ts)
    token += AES_PUBKEY[15:]
    token += b"\x00\x01\x00\x00\x00"
    token += struct.pack("<I", zlib.crc32(CRC_SALT + bytes(token)) & 0xFFFFFFFF)
    return base64.b64encode(bytes(token)).decode("ascii")


def make_sig(userkey: str, userid: str, fileid: str, target: str) -> str:
    h = sha1(userkey.encode("ascii"))
    h.update(b2a_hex(sha1(f"{userid}{fileid}{target}0".encode("ascii")).digest()))
    h.update(b"000000")
    return h.hexdigest().upper()


def make_token(fileid, filesize, sign_key, sign_val, userid, t, appversion) -> str:
    m = md5(MD5_SALT)
    m.update(f"{fileid}{filesize}{sign_key}{sign_val}{userid}{t}".encode("ascii"))
    m.update(b2a_hex(md5(b"%d" % int(userid)).digest()))
    m.update(appversion.encode("ascii"))
    return m.hexdigest()


def urlencode_sorted(d: dict) -> str:
    """模拟 Python urllib.parse.urlencode(sorted(items))，空值被过滤。"""
    return "&".join(
        f"{quote_plus(str(k))}={quote_plus(str(v))}"
        for k, v in sorted(d.items())
        if v
    )


def main() -> int:
    out = {}

    # 1) k_ec token：固定时间戳，便于回归
    ts = 1730000000
    out["token"] = {
        "timestamp": ts,
        "k_ec": ecdh_encode_token(ts),
    }

    # 2) 签名：固定输入
    userkey = "0123456789abcdef0123456789abcdef"
    userid = "342204514"
    fileid = "DA39A3EE5E6B4B0D3255BFEF95601890AFD80709"
    target = "U_1_2491055587586025989"
    out["sig"] = {
        "userkey": userkey,
        "userid": userid,
        "fileid": fileid,
        "target": target,
        "value": make_sig(userkey, userid, fileid, target),
    }

    # 3) token（请求体里的 token 字段）
    out["body_token"] = {
        "fileid": fileid,
        "filesize": 1024,
        "sign_key": "",
        "sign_val": "",
        "userid": userid,
        "t": ts,
        "appversion": "36.2.28",
        "value": make_token(fileid, 1024, "", "", userid, ts, "36.2.28"),
    }

    # 4) 完整 payload 的待加密明文（已 urlencode，供 AES 对比）
    payload = {
        "appid": 0,
        "target": target,
        "sign_key": "",
        "sign_val": "",
        "topupload": "true",
        "appversion": "36.2.28",
        "userid": userid,
        "userkey": userkey,
        "fileid": fileid,
        "filename": "测试 视频.mp4",
        "filesize": 1024,
        "path": "",
        "t": ts,
        "sig": make_sig(userkey, userid, fileid, target),
        "token": make_token(fileid, 1024, "", "", userid, ts, "36.2.28"),
    }
    plain = urlencode_sorted(payload)
    out["payload_plain"] = plain

    # 5) AES 密钥/IV（固定常量，测试用）
    out["aes"] = {
        "key_hex": "fb1a19d652f5aaf7bc651d0f69bf422f",
        "iv_hex": "69bf422f49960550a0ad44ec3446cb4c",
    }

    json.dump(out, sys.stdout, ensure_ascii=False, indent=2)
    sys.stdout.write("\n")

    # 明文另存，供 openssl 直接加密（注意：Windows 下没有 /tmp，写到脚本同目录）
    out_path = os.path.join(os.path.dirname(os.path.abspath(__file__)), "_payload_plain.bin")
    with open(out_path, "wb") as f:
        f.write(plain.encode("latin-1"))
    sys.stderr.write(f"plain saved: {out_path} ({len(plain)} bytes)\n")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
