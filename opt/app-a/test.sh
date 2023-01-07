#!/usr/bin/env bash

set -eu

sleep 5

RESULT=$(./build_dir/build/bin/math_parser-bin "4+4")

if [[ "$RESULT" != "8" ]]; then
  exit 1
fi
