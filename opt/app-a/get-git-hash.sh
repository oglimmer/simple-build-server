#!/usr/bin/env bash

set -eu

git ls-remote "$(./get-git-url.sh)" master | cut -f1
