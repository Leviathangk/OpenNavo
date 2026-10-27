"""Digest compatibility for optional server-side partial-update state checks."""

import hashlib
import json
from typing import Any


def state_hash(value: Any) -> str:
    return hashlib.sha256(
        json.dumps(value, sort_keys=True, separators=(",", ":")).encode()
    ).hexdigest()
