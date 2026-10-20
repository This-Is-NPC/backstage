"""Measure isolated 60-second visual workloads with fresh render caches."""

import argparse
import json
import os
from pathlib import Path
import shutil
import statistics
import subprocess
import tempfile


parser = argparse.ArgumentParser(description=__doc__)
parser.add_argument("binary", type=Path)
parser.add_argument(
    "feature", nargs="?", default="cards",
    choices=["cards", "camera", "actions", "release", "captions", "transitions", "audio", "all"],
)
args = parser.parse_args()
binary = str(args.binary.resolve())
source = Path(__file__).resolve().parent
features = ["cards", "camera", "actions", "release", "captions", "transitions", "audio"] if args.feature == "all" else [args.feature]


def ffmpeg(*options):
    subprocess.run(["ffmpeg", "-v", "error", "-y", *options], check=True)


def workload(feature):
    if feature in {"cards", "camera", "actions", "release"}:
        document = json.loads((source / f"{feature}.json").read_text())
        scale = 60 / document["duration"]
        document["duration"] = 60
        for event in document.get("timeline", []):
            event["at"] *= scale
        for track in document.get("tracks", {}).values():
            for key in track.get("camera", {}).get("keyframes", []):
                key["at"] *= scale
        for demo in document.get("release", {}).get("demos", []):
            demo["duration"] *= scale
        return document
    document = {
        "version": 1, "duration": 60,
        "render": {"w": 1280, "h": 720, "fps": 30},
        "timeline": [{"at": 0, "layout": "single"}],
    }
    if feature == "captions":
        document["captions"] = [
            {"scene": "explain", "cue": f"cue-{i}", "clock": "presentation", "at": i * 3,
             "duration": 2, "slot": "subtitle", "entrance": {"effect": "rise"}}
            for i in range(20)
        ]
    elif feature == "transitions":
        document["timeline"] = [
            {"at": i * 3, "card": {"type": "title", "title": f"Update {i + 1}", "entrance": {"effect": "none"}},
             **({"transition": {"effect": "push", "duration": 8 / 30}} if i else {})}
            for i in range(20)
        ]
    elif feature == "audio":
        document["audio"] = [
            {"id": "music", "file": "music.wav", "role": "music", "duck": True},
            *[{"id": f"voice-{i}", "file": "voice.wav", "at": i * 3 + 0.5, "role": "narration"} for i in range(20)],
        ]
    return document


with tempfile.TemporaryDirectory(prefix="backstage-visual-fixture-") as fixture_dir:
    fixture = Path(fixture_dir)
    if any(feature in {"camera", "actions", "release"} for feature in features):
        ffmpeg("-f", "lavfi", "-i", "testsrc2=s=1280x720:r=30:d=60", "-c:v", "libx264", "-preset", "veryfast", "-crf", "20", "-threads", "4", str(fixture / "demo.mp4"))
        steps = [
            {"step": f"action-{i}", "action": "keys", "start": i * 3 + 0.5, "end": i * 3 + 2.5,
             "keys": ["ctrl+s"], "region": {"x": 0.25, "y": 0.25, "w": 0.5, "h": 0.5},
             "pointer": {"x": 0.5, "y": 0.5, "click": True}}
            for i in range(20)
        ]
        (fixture / "demo.facts.json").write_text(json.dumps({"origin": "synthetic-benchmark-fixture", "steps": steps}))
    if "audio" in features:
        for name, frequency, duration in [("music", 220, 60), ("voice", 2200, 2)]:
            ffmpeg("-f", "lavfi", "-i", f"sine=frequency={frequency}:duration={duration}:sample_rate=48000", str(fixture / f"{name}.wav"))
    for feature in features:
        seconds = []
        for iteration in range(3):
            with tempfile.TemporaryDirectory(prefix="backstage-visual-bench-") as directory:
                root = Path(directory)
                for asset in fixture.iterdir():
                    shutil.copyfile(asset, root / asset.name)
                (root / "backstage.json").write_text(json.dumps({"presentations": {"show": {"file": "show.json", "out": "exports/show.mp4"}}}))
                (root / "show.json").write_text(json.dumps(workload(feature)))
                if feature == "captions":
                    (root / "scenes").mkdir()
                    (root / "visual.html").write_text("<html></html>")
                    (root / "scenes/explain.json").write_text(json.dumps({"type": "visual", "entry": "visual.html", "duration": 60, "narration": {"cues": [{"id": f"cue-{i}", "start": i * 3, "end": i * 3 + 2, "text": "A simpler workflow"} for i in range(20)]}}))
                environment = dict(os.environ, XDG_CACHE_HOME=str(root / "cache"))
                subprocess.run([binary, "--project", directory, "render", "show"], env=environment, stdout=subprocess.DEVNULL, check=True)
                facts = json.loads((root / "exports/show.facts.json").read_text())
                timings = facts["timings"]
                seconds.append(timings["total-seconds"])
                print(json.dumps({"feature": feature, "iteration": iteration + 1, "frames": facts["render"]["frames"], "seconds": seconds[-1], "layered-chunks": timings.get("layered-chunks", 0), "workers": timings["workers"], "animated-overlay-frames": timings.get("animated-overlay-frames", 0)}), flush=True)
        print(json.dumps({"feature": feature, "median-seconds": statistics.median(seconds)}), flush=True)
