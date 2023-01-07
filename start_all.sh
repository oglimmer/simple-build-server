#!/bin/bash

set -eu

# cron always starts as a deamon
cron

httpd-foreground
