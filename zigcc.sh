#!/bin/sh
exec python3 -m ziglang cc -target arm-linux-musleabihf "$@"
