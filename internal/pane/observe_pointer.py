"""Stream read-only Hyprland positions and mouse button presses as JSON lines."""

import glob
import json
import os
import select
import socket
import struct
import sys
import time


def query(command):
    path = os.path.join(os.environ["XDG_RUNTIME_DIR"], "hypr",
                        os.environ["HYPRLAND_INSTANCE_SIGNATURE"], ".socket.sock")
    with socket.socket(socket.AF_UNIX) as conn:
        conn.settimeout(0.2)
        conn.connect(path)
        conn.sendall(("j/" + command).encode())
        chunks = []
        while True:
            chunk = conn.recv(65536)
            if not chunk:
                return json.loads(b"".join(chunks))
            chunks.append(chunk)


def devices(current):
    # Capability bitmaps are printed most significant word first. Only devices
    # advertising BTN_LEFT are opened; keyboard events are never serialized.
    for path in glob.glob("/sys/class/input/event*/device/capabilities/key"):
        name = path.split("/")[-4]
        if name in current:
            continue
        try:
            with open(path, encoding="ascii") as source:
                bits = int("".join(word.zfill(struct.calcsize("L") * 2)
                                   for word in source.read().split()), 16)
            if bits & (1 << 0x110):
                current[name] = [os.open("/dev/input/" + name,
                                        os.O_RDONLY | os.O_NONBLOCK), False]
        except (OSError, ValueError):
            pass


def main():
    origin = time.monotonic()
    event = struct.Struct("llHHi")
    opened = {}
    monitor = sys.argv[1]
    last_scan = -10
    next_sample = origin
    while True:
        time.sleep(max(0, next_sample - time.monotonic()))
        next_sample = time.monotonic() + 1 / 30
        now = time.monotonic()
        if now - last_scan >= 1:
            devices(opened)
            last_scan = now
        ready, _, _ = select.select([state[0] for state in opened.values()], [], [], 0)
        click = False
        for name, state in list(opened.items()):
            if state[0] not in ready:
                continue
            try:
                data = os.read(state[0], event.size * 4096)
                if not data:
                    raise OSError("input device disconnected")
                for _, _, kind, code, value in event.iter_unpack(data):
                    if kind == 0 and code == 3:  # SYN_DROPPED
                        state[1] = True
                    elif state[1]:
                        if kind == 0 and code == 0:  # SYN_REPORT
                            state[1] = False
                    elif kind == 1 and 0x110 <= code <= 0x117 and value == 1:
                        click = True
            except BlockingIOError:
                pass
            except OSError:
                os.close(state[0])
                del opened[name]
        message = {}
        try:
            outputs = query("monitors")
            selected = [item for item in outputs if item["name"] == monitor] if monitor else outputs
            if len(selected) == 1:
                message.update(monitor=selected[0], pointer=query("cursorpos"), click=click)
        except (OSError, ValueError, KeyError):
            pass
        message["elapsed"] = time.monotonic() - origin
        print(json.dumps(message, separators=(",", ":")), flush=True)


if __name__ == "__main__":
    try:
        main()
    except (BrokenPipeError, KeyboardInterrupt):
        pass
