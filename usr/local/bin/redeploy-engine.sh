#!/usr/bin/env bash

set -eu

. /etc/redeploy.conf

for BUILD_NAME in "${!SYSTEM_PASSWORDS[@]}"
do
  if [ -f "/var/lib/redeploy/schedule-$BUILD_NAME" ]; then
    echo "$(date +"%Y-%m-%dT%H:%M:%S%z") Found /var/lib/redeploy/schedule-$BUILD_NAME $(cat "/var/lib/redeploy/schedule-$BUILD_NAME")"
    rm /var/lib/redeploy/schedule-$BUILD_NAME

    if [ -f "/var/lib/redeploy/running-$BUILD_NAME" ]; then
      echo "$(date +"%Y-%m-%dT%H:%M:%S%z") Found /var/lib/redeploy/running-$BUILD_NAME sending SIGKILL to PID $(cat "/var/lib/redeploy/running-$BUILD_NAME")"
      set +e
      kill -9 "$(cat "/var/lib/redeploy/running-$BUILD_NAME")"
      set -e
    fi
    START_TIME=$(date)
	  START_TIME_MILLIS=$(date +%s)
    cd "/opt/$BUILD_NAME"
	      
    if [ -f "./get-git-hash.sh" ]; then
      LAST_HASH=$(./get-git-hash.sh)
    else
      LAST_HASH=""
    fi
    if [ -f "./get-git-url.sh" ]; then
      GIT_REPO_URL=$(./get-git-url.sh)
    else
      GIT_REPO_URL=""
    fi
    RUN_LOG_FILE=/var/log/redeploy-build-$BUILD_NAME-$START_TIME_MILLIS.log
    
    {
      echo "START_TIME=\"$START_TIME\""
      echo "RUN_LOG_FILE=$RUN_LOG_FILE"
      echo "GIT_REPO_URL=$GIT_REPO_URL"
      echo "LAST_HASH=$LAST_HASH"
    } > "/var/lib/redeploy/lastdeploy-$BUILD_NAME"
    if [ ! -f ./test.sh ]; then
      echo "STATUS_TEST=no-test-defined" >> "/var/lib/redeploy/lastdeploy-$BUILD_NAME"
    fi

    ./build.sh &>> "$RUN_LOG_FILE" &
    PID=$!
    echo "$PID" > "/var/lib/redeploy/running-$BUILD_NAME"

    echo "$(date +"%Y-%m-%dT%H:%M:%S%z") Sub-shell started with PID $PID logging into $RUN_LOG_FILE"

    set +e
    wait $PID 
    STATUS=$?
    set -e
    echo "STATUS=$STATUS" >> "/var/lib/redeploy/lastdeploy-$BUILD_NAME"
    echo "$(date +"%Y-%m-%dT%H:%M:%S%z") Sub-shell finished with status = $STATUS with PID = $PID"

    if [ -f ./test.sh ]; then
      ./test.sh &>> "$RUN_LOG_FILE" &
      PID=$!
      echo "$PID" > "/var/lib/redeploy/running-$BUILD_NAME"

      echo "$(date +"%Y-%m-%dT%H:%M:%S%z") Sub-shell started with PID $PID logging into $RUN_LOG_FILE"

      set +e
      wait $PID
      STATUS_TEST=$?
      set -e
      echo "STATUS_TEST=$STATUS_TEST" >> "/var/lib/redeploy/lastdeploy-$BUILD_NAME"
      echo "$(date +"%Y-%m-%dT%H:%M:%S%z") Sub-shell finished with testing status = $STATUS_TEST with PID = $PID"
    else
      rm "/var/lib/redeploy/running-$BUILD_NAME"
    fi

    END_DATE="$(date)"
    echo "END_DATE=\"$END_DATE\"" >> "/var/lib/redeploy/lastdeploy-$BUILD_NAME"
  fi
done
