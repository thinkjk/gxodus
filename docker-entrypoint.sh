#!/bin/sh
set -e

# Use GXODUS_COMMAND env var if set, otherwise fall back to first arg, then "export"
COMMAND="${GXODUS_COMMAND:-${1:-export}}"

# Build common args
CONFIG_DIR="${GXODUS_CONFIG_DIR:-/config}"
CONFIG_ARG="--config"
CONFIG_VAL="${CONFIG_DIR}/config.toml"
SESSION_FILE="${CONFIG_DIR}/session.enc"

echo "gxodus: command=$COMMAND"
echo "gxodus: config=$CONFIG_VAL"
echo "gxodus: remote_chrome=${GXODUS_REMOTE_CHROME:-(not set)}"

run_auth() {
    if [ -n "$GXODUS_REMOTE_CHROME" ]; then
        echo "gxodus: authenticating via remote chrome..."
        gxodus auth --remote-chrome "$GXODUS_REMOTE_CHROME" "$CONFIG_ARG" "$CONFIG_VAL"
    else
        echo "Starting noVNC for interactive authentication..."
        echo "Access the browser at: http://localhost:6080/vnc.html"

        Xvfb :99 -screen 0 1280x720x24 &
        sleep 1
        fluxbox &
        x11vnc -display :99 -nopw -forever -shared -rfbport 5900 &
        websockify --web /usr/share/novnc 6080 localhost:5900 &
        sleep 1

        gxodus auth "$CONFIG_ARG" "$CONFIG_VAL"
    fi
}

build_export_args() {
    ARGS=""
    [ -n "$GXODUS_FILE_SIZE" ] && ARGS="$ARGS --file-size $GXODUS_FILE_SIZE"
    [ -n "$GXODUS_POLL_INTERVAL" ] && ARGS="$ARGS --poll-interval $GXODUS_POLL_INTERVAL"
    [ "$GXODUS_EXTRACT" = "true" ] && ARGS="$ARGS --extract"
    [ "$GXODUS_NO_KEEP_ZIP" = "true" ] && ARGS="$ARGS --no-keep-zip"
    echo "$ARGS"
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

    EXPORT_ARGS=$(build_export_args)

    if [ -n "$GXODUS_REMOTE_CHROME" ]; then
        exec gxodus export --output "${GXODUS_OUTPUT_DIR:-/exports}" --remote-chrome "$GXODUS_REMOTE_CHROME" "$CONFIG_ARG" "$CONFIG_VAL" $EXPORT_ARGS
    else
        exec gxodus export --output "${GXODUS_OUTPUT_DIR:-/exports}" "$CONFIG_ARG" "$CONFIG_VAL" $EXPORT_ARGS
    fi

elif [ "$COMMAND" = "status" ]; then
    if [ -n "$GXODUS_REMOTE_CHROME" ]; then
        exec gxodus status --remote-chrome "$GXODUS_REMOTE_CHROME" "$CONFIG_ARG" "$CONFIG_VAL"
    else
        exec gxodus status "$CONFIG_ARG" "$CONFIG_VAL"
    fi

else
    exec gxodus "$@"
fi
