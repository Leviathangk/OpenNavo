#!/usr/bin/env python3
"""Stage native bundle contents without running or installing helpers."""
from pathlib import Path
import subprocess
import sys

scripts = Path(__file__).resolve().parent
for script in ['prepare-fan-helper.py', 'prepare-security-helpers.py', 'prepare-store-helper.py']:
    subprocess.run([sys.executable, str(scripts / script), '--allow-unsigned'], check=True)
