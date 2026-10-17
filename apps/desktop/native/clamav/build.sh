#!/bin/sh
# Build only in the supplied scratch directory; never installs into the host.
set -eu
exec python3 "$(dirname "$0")/build.py" "$@"
