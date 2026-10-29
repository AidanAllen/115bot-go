"""Telegram Bot API 模拟器，用于本地冒烟测试（不连真实 Telegram）。

覆盖范围：getMe / getUpdates / sendMessage / editMessageText /
answerCallbackQuery / editMessageReplyMarkup / setMyCommands。

工作方式：
  1. 先投递 /id，随后把 bot 每条回复里出现的内联按钮依次当作「用户点击」投递回去。
     这样能自动遍历所有按钮，证明「点下去都有反应」，而不是只看静态文本。
  2. 需要真实访问 115 的按钮（扫码登录、检查登录、账号初始化）会被跳过，
     否则本地跑一次要等好几个网络超时。它们的处理分支由单元测试覆盖。
  3. 队列耗尽且一段时间没有新回复后，写 summary.json 并自动退出。

用法：
    python scripts/smoke_bot.py 18081

配合：
    TG_API_ENDPOINT=http://127.0.0.1:18081/bot%s/%s BOT_TOKEN=1:test \
    DB_PATH=/tmp/smoke/db.sqlite TEMP_DIR=/tmp/smoke/tmp ./115bot
"""
import json
import os
import re
import sys
import threading
import time
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from urllib.parse import unquote_plus

OUT = os.environ.get("SMOKE_OUT", "/tmp/smoke")
os.makedirs(OUT, exist_ok=True)

OWNER = int(os.environ.get("SMOKE_OWNER_ID", "999"))

# 触发后会真实访问 115 的回调：本地冒烟跳过，避免每个都等一个网络超时
SKIP_CALLBACKS = (
    "act:login", "login:", "act:checklogin", "act:init",
    "cmd:login", "cmd:checklogin", "cmd:initaccount",
)

# 投递上限，避免按钮成环时无限循环
MAX_UPDATES = 80

STATE = {
    "poll": 0,
    "msg_id": 100,
    "replies": [],
    "queue": [],
    "seen_buttons": set(),
    "sent_callbacks": [],
    "commands": None,
    "idle": 0,
    "stop": False,
    "last_activity": time.time(),
}

# 队列空且距最后一次 bot 动作超过这么久，才认为跑完。
# 不能用「连续空轮询次数」判断：bot 处理一条命令期间 mock 会反复返回空数组，
# 次数一到就提前收工，后面的按钮根本没机会点。
IDLE_SECONDS = 8


def message_update(text, mid):
    cmd = text.split()[0]
    return {
        "update_id": mid,
        "message": {
            "message_id": mid,
            "date": int(time.time()),
            "chat": {"id": OWNER, "type": "private", "first_name": "Tester"},
            "from": {"id": OWNER, "is_bot": False, "first_name": "Tester"},
            "text": text,
            "entities": [{"type": "bot_command", "offset": 0, "length": len(cmd)}],
        },
    }


def callback_update(data, mid):
    return {
        "update_id": mid,
        "callback_query": {
            "id": str(mid),
            "from": {"id": OWNER, "is_bot": False, "first_name": "Tester"},
            "chat_instance": "smoke",
            "data": data,
            "message": {
                "message_id": mid,
                "date": int(time.time()),
                "chat": {"id": OWNER, "type": "private", "first_name": "Tester"},
                "from": {"id": 1, "is_bot": True, "first_name": "Smoke", "username": "smoke_bot"},
                "text": "菜单",
            },
        },
    }


def after_reply(text):
    """按已出现的回复决定后续要投什么。

    绑定完成前投 /help、/start 会被鉴权拦下（回复「尚未绑定操作 id」），
    所以必须等「绑定成功」出现后再继续。
    """
    if "绑定成功" in text and not STATE.get("bound"):
        STATE["bound"] = True
        # /login apps 只渲染按钮，不打网络，可以安全地验证设备选择面板
        STATE["queue"].extend([("msg", "/help"), ("msg", "/start"),
                               ("msg", "/nope"), ("msg", "/login apps")])


def enqueue_buttons(markup):
    """把回复里的内联按钮排进投递队列（去重、跳过会打网络的）。"""
    try:
        kb = json.loads(markup)["inline_keyboard"]
    except Exception:
        return 0
    n = 0
    for row in kb:
        for b in row:
            data = b.get("callback_data")
            if not data or data in STATE["seen_buttons"]:
                continue
            STATE["seen_buttons"].add(data)
            if data.startswith(SKIP_CALLBACKS):
                continue
            STATE["queue"].append(("cb", data))
            n += 1
    return n


class Handler(BaseHTTPRequestHandler):
    # HTTP/1.0：每次响应后断开，避免长轮询下响应体错位
    protocol_version = "HTTP/1.0"

    def log_message(self, *args):
        pass

    def _json(self, obj, code=200):
        body = json.dumps(obj).encode()
        self.send_response(code)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def _send_updates(self):
        if STATE["poll"] == 0:
            STATE["poll"] = 1
            # 先只投 /id：未绑定时它会带「绑定为我」按钮，
            # 绑定成功后才会继续投 /help 与 /start（见 after_reply）。
            STATE["queue"].append(("msg", "/id"))

        if STATE["queue"] and STATE["poll"] < MAX_UPDATES:
            kind, payload = STATE["queue"].pop(0)
            STATE["poll"] += 1
            upd = callback_update(payload, 1000 + STATE["poll"]) if kind == "cb" \
                else message_update(payload, 1000 + STATE["poll"])
            if kind == "cb":
                STATE["sent_callbacks"].append(payload)
            STATE["idle"] = 0
            STATE["last_activity"] = time.time()
            self._json({"ok": True, "result": [upd]})
            return

        STATE["idle"] += 1
        time.sleep(0.2)
        self._json({"ok": True, "result": []})
        if time.time() - STATE["last_activity"] > IDLE_SECONDS:
            STATE["stop"] = True
            threading.Thread(target=self.server.shutdown, daemon=True).start()

    def _parse_multipart(self, raw):
        """把 multipart/form-data 解析成 name -> bytes；非 multipart 时按 urlencoded 处理。"""
        ctype = self.headers.get("Content-Type") or ""
        m = re.search(r"boundary=(\S+)", ctype)
        if "multipart" not in ctype or not m:
            out = {}
            for pair in raw.decode("utf-8", "replace").split("&"):
                if "=" in pair:
                    k, v = pair.split("=", 1)
                    out[k] = unquote_plus(v).encode()
            return out

        boundary = ("--" + m.group(1).strip('"')).encode()
        out = {}
        for part in raw.split(boundary):
            head, _, body = part.partition(b"\r\n\r\n")
            nm = re.search(rb'name="([^"]+)"', head)
            if not nm:
                continue
            out[nm.group(1).decode()] = body.rstrip(b"\r\n")
        return out

    def _read_body(self):
        if self.headers.get("Transfer-Encoding", "").lower() == "chunked":
            chunks = []
            while True:
                line = self.rfile.readline().strip()
                if not line:
                    continue
                size = int(line, 16)
                if size == 0:
                    self.rfile.readline()
                    break
                chunks.append(self.rfile.read(size))
                self.rfile.readline()
            return b"".join(chunks)
        n = int(self.headers.get("Content-Length") or 0)
        return self.rfile.read(n) if n else b""

    def do_GET(self):
        m = re.match(r"^/bot([^/]+)/(\w+)$", self.path)
        if not m:
            self._json({"ok": False, "description": "bad path"}, 404)
            return
        method = m.group(2)

        if method == "getMe":
            self._json({"ok": True, "result": {
                "id": 1, "is_bot": True, "first_name": "Smoke", "username": "smoke_bot"}})
            return

        if method == "getUpdates":
            self._send_updates()
            return

        self._json({"ok": True, "result": True})

    def do_POST(self):
        m = re.match(r"^/bot([^/]+)/(\w+)$", self.path)
        if not m:
            self._json({"ok": False, "description": "bad path"}, 404)
            return
        method = m.group(2)
        raw = self._read_body()

        if method == "getMe":
            self._json({"ok": True, "result": {
                "id": 1, "is_bot": True, "first_name": "Smoke", "username": "smoke_bot"}})
            return

        if method == "getUpdates":
            self._send_updates()
            return

        if method == "setMyCommands":
            # commands 是以 JSON 字符串表单字段提交的
            fields = self._parse_multipart(raw)
            raw_cmds = fields.get("commands", b"").decode("utf-8", "replace")
            try:
                STATE["commands"] = json.loads(raw_cmds)
            except Exception:
                STATE["commands"] = raw_cmds
            self._json({"ok": True, "result": True})
            return

        if method in ("sendMessage", "editMessageText"):
            # tgbotapi 用 multipart/form-data 提交，不能按 JSON 解析
            fields = self._parse_multipart(raw)
            text = fields.get("text", b"").decode("utf-8", "replace")
            markup = fields.get("reply_markup", b"").decode("utf-8", "replace")
            enqueue_buttons(markup)
            STATE["msg_id"] += 1
            STATE["last_activity"] = time.time()
            after_reply(text)
            STATE["replies"].append({
                "method": method,
                "text": text,
                "buttons": [
                    b.get("text") for row in
                    (json.loads(markup).get("inline_keyboard", []) if markup else [])
                    for b in row
                ],
            })
            self._json({"ok": True, "result": {
                "message_id": STATE["msg_id"],
                "date": int(time.time()),
                "chat": {"id": OWNER, "type": "private"},
                "text": text,
            }})
            return

        self._json({"ok": True, "result": True})


def dump():
    with open(os.path.join(OUT, "replies.json"), "w", encoding="utf-8") as f:
        json.dump(STATE["replies"], f, ensure_ascii=False, indent=1)
    with open(os.path.join(OUT, "summary.json"), "w", encoding="utf-8") as f:
        json.dump({
            "registered_commands": STATE["commands"],
            "clicks_sent": STATE["sent_callbacks"],
            "replies": len(STATE["replies"]),
        }, f, ensure_ascii=False, indent=1)


def main():
    port = int(sys.argv[1]) if len(sys.argv) > 1 else 18081
    srv = ThreadingHTTPServer(("127.0.0.1", port), Handler)
    print(f"mock telegram api on http://127.0.0.1:{port}", flush=True)
    try:
        srv.serve_forever()
    finally:
        dump()
        print(f"done: {len(STATE['replies'])} replies, "
              f"{len(STATE['sent_callbacks'])} clicks", flush=True)


if __name__ == "__main__":
    main()
