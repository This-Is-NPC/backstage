#!/usr/bin/env python3
"""Generate reproducible local inputs; no VMs or network are required."""
from pathlib import Path
import subprocess
root = Path(__file__).resolve().parent
(root / 'recordings').mkdir(exist_ok=True)
(root / 'assets').mkdir(exist_ok=True)
for name, color in [('parent', '0x345678'), ('laptop', '0x664488'), ('browser', '0x337755')]:
    subprocess.run(['ffmpeg', '-v', 'error', '-y', '-f', 'lavfi', '-i',
                    f'color=c={color}:s=640x360:r=24:d=4', '-vf',
                    f"drawtext=text='{name} frame %{{n}}':fontcolor=white:fontsize=32:x=20:y=150",
                    '-c:v', 'libx264', '-pix_fmt', 'yuv420p',
                    str(root / 'recordings' / f'{name}.mp4')], check=True)
for name, freq in [('voice', 660), ('music', 220)]:
    subprocess.run(['ffmpeg', '-v', 'error', '-y', '-f', 'lavfi', '-i',
                    f'sine=frequency={freq}:sample_rate=48000:duration=1',
                    str(root / 'assets' / f'{name}.wav')], check=True)
