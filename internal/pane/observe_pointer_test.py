import importlib.util
import io
import json
import pathlib
import struct
import unittest
from unittest.mock import patch

spec = importlib.util.spec_from_file_location("observer", pathlib.Path(__file__).with_name("observe_pointer.py"))
observer = importlib.util.module_from_spec(spec)
spec.loader.exec_module(observer)


class PointerEvents(unittest.TestCase):
    def capture(self, events):
        packet = b"".join(struct.pack("llHHi", 0, 0, *event) for event in events)
        output = io.StringIO()

        def devices(current):
            current["event0"] = [9, False]

        def query(command):
            if command == "monitors":
                return [{"name": "test", "width": 800, "height": 600, "scale": 1}]
            return {"x": 200, "y": 150}

        with patch.object(observer, "devices", devices), patch.object(observer, "query", query), \
                patch.object(observer.sys, "argv", ["observer", "test"]), \
                patch.object(observer.sys, "stdout", output), \
                patch.object(observer.time, "sleep", side_effect=[None, KeyboardInterrupt]), \
                patch.object(observer.select, "select", return_value=([9], [], [])), \
                patch.object(observer.os, "read", return_value=packet):
            with self.assertRaises(KeyboardInterrupt):
                observer.main()
        return json.loads(output.getvalue())

    def test_actual_mouse_presses_only(self):
        self.assertTrue(self.capture([(1, 0x110, 1)])["click"])
        for events in [[(1, 30, 1)], [(1, 0x110, 0)], [(1, 0x110, 2)],
                       [(0, 3, 0), (1, 0x110, 1), (0, 0, 0)]]:
            self.assertFalse(self.capture(events)["click"])
        self.assertTrue(self.capture([(0, 3, 0), (1, 0x110, 1), (0, 0, 0), (1, 0x111, 1)])["click"])

    def test_device_discovery_uses_mouse_capabilities(self):
        bits = 1 << 0x110
        width = struct.calcsize("L") * 8
        words = []
        while bits:
            words.insert(0, format(bits & ((1 << width) - 1), "x"))
            bits >>= width
        with patch.object(observer.glob, "glob", return_value=["/sys/class/input/event5/device/capabilities/key"]), \
                patch("builtins.open", return_value=io.StringIO(" ".join(words))), \
                patch.object(observer.os, "open", return_value=7) as opened:
            found = {}
            observer.devices(found)
        self.assertEqual(found, {"event5": [7, False]})
        opened.assert_called_once_with("/dev/input/event5", observer.os.O_RDONLY | observer.os.O_NONBLOCK)


if __name__ == "__main__":
    unittest.main()
