#!/bin/sh
set -e

# Use GXODUS_COMMAND env var if set, otherwise fall back to first arg, then "export"
COMMAND="${GXODUS_COMMAND:-${1:-export}}"

# Build common args
CONFIG_DIR="${GXODUS_CONFIG_DIR:-/config}"
CONFIG_ARG="--config ${CONFIG_DIR}/config.toml"
SESSION_FILE="${CONFIG_DIR}/session.enc"

CHROME_ARG=""
[ -n "$GXODUS_REMOTE_CHROME" ] && CHROME_ARG="--remote-chrome $GXODUS_REMOTE_CHROME"

run_auth() {
    if [ -n "$GXODUS_REMOTE_CHROME" ]; then
        gxodus auth $CHROME_ARG $CONFIG_ARG
    else
        echo "Starting noVNC for interactive authentication..."
        echo "Access the browser at: http://localhost:6080/vnc.html"

        Xvfb :99 -screen 0 1280x720x24 &
        sleep 1
        fluxbox &
        x11vnc -display :99 -nopw -forever -shared -rfbport 5900 &
        websockify --web /usr/share/novnc 6080 localhost:5900 &
        sleep 1

        gxodus auth $CONFIG_ARG
    fi
}

if [ "$COMMAND" = "auth" ]; then
    run_auth

elif [ "$COMMAND" = "export" ]; then
    # Auto-auth if no session exists
    if [ ! -f "$SESSION_FILE" ]; then
        echo "No session found. Starting authentication first..."
        run_auth
        echo ""
        echo "Authentication complete. Starting export..."
    fi

    EXTRA_ARGS=""
    [ -n "$GXODUS_FILE_SIZE" ] && EXTRA_ARGS="$EXTRA_ARGS --file-size $GXODUS_FILE_SIZE"
    [ -n "$GXODUS_POLL_INTERVAL" ] && EXTRA_ARGS="$EXTRA_ARGS --poll-interval $GXODUS_POLL_INTERVAL"
    [ "$GXODUS_EXTRACT" = "true" ] && EXTRA_ARGS="$EXTRA_ARGS --extract"
    [ "$GXODUS_NO_KEEP_ZIP" = "true" ] && EXTRA_ARGS="$EXTRA_ARGS --no-keep-zip"

    exec gxodus export --output "${GXODUS_OUTPUT_DIR:-/exports}" $CHROME_ARG $CONFIG_ARG $EXTRA_ARGS

elif [ "$COMMAND" = "status" ]; then
    exec gxodus status $CHROME_ARG $CONFIG_ARG

else
    exec gxodus "$@"
fi
