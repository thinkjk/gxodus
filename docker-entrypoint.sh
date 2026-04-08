#!/bin/sh
set -e

COMMAND="${1:-export}"

# Build common args
CONFIG_ARG="--config ${GXODUS_CONFIG_DIR:-/config}/config.toml"

if [ "$COMMAND" = "auth" ] && [ -z "$GXODUS_REMOTE_CHROME" ]; then
    echo "Starting noVNC for interactive authentication..."
    echo "Access the browser at: http://localhost:6080/vnc.html"

    Xvfb :99 -screen 0 1280x720x24 &
    sleep 1
    fluxbox &
    x11vnc -display :99 -nopw -forever -shared -rfbport 5900 &
    websockify --web /usr/share/novnc 6080 localhost:5900 &
    sleep 1

    exec gxodus auth $CONFIG_ARG

elif [ "$COMMAND" = "auth" ] && [ -n "$GXODUS_REMOTE_CHROME" ]; then
    exec gxodus auth --remote-chrome "$GXODUS_REMOTE_CHROME" $CONFIG_ARG

elif [ "$COMMAND" = "export" ]; then
    EXTRA_ARGS=""
    [ -n "$GXODUS_REMOTE_CHROME" ] && EXTRA_ARGS="$EXTRA_ARGS --remote-chrome $GXODUS_REMOTE_CHROME"
    [ -n "$GXODUS_FILE_SIZE" ] && EXTRA_ARGS="$EXTRA_ARGS --file-size $GXODUS_FILE_SIZE"
    [ -n "$GXODUS_POLL_INTERVAL" ] && EXTRA_ARGS="$EXTRA_ARGS --poll-interval $GXODUS_POLL_INTERVAL"
    [ "$GXODUS_EXTRACT" = "true" ] && EXTRA_ARGS="$EXTRA_ARGS --extract"
    [ "$GXODUS_NO_KEEP_ZIP" = "true" ] && EXTRA_ARGS="$EXTRA_ARGS --no-keep-zip"

    exec gxodus export --output "${GXODUS_OUTPUT_DIR:-/exports}" $CONFIG_ARG $EXTRA_ARGS

elif [ "$COMMAND" = "status" ]; then
    EXTRA_ARGS=""
    [ -n "$GXODUS_REMOTE_CHROME" ] && EXTRA_ARGS="$EXTRA_ARGS --remote-chrome $GXODUS_REMOTE_CHROME"
    exec gxodus status $CONFIG_ARG $EXTRA_ARGS

else
    exec gxodus "$@"
fi
