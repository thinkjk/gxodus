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

# Auth always uses noVNC with local Chrome so the user can interact with the
# browser to complete Google login. Remote Chrome (browserless) is only used
# for headless commands (export, status).
run_auth() {
    echo "Starting noVNC for interactive authentication..."
    echo "Access the browser at: http://<your-unraid-ip>:6080/vnc.html"

    mkdir -p /tmp/.X11-unix && chmod 1777 /tmp/.X11-unix
    rm -f /tmp/.X99-lock

    Xvfb :99 -screen 0 1280x720x24 -ac >/tmp/xvfb.log 2>&1 &
    XVFB_PID=$!

    for i in $(seq 1 30); do
        [ -S /tmp/.X11-unix/X99 ] && break
        if ! kill -0 "$XVFB_PID" 2>/dev/null; then
            echo "ERROR: Xvfb died on startup. Log:"
            cat /tmp/xvfb.log
            return 1
        fi
        sleep 0.2
    done

    [ -S /tmp/.X11-unix/X99 ] || { echo "ERROR: Xvfb not ready in 6s"; cat /tmp/xvfb.log; return 1; }

    fluxbox >/tmp/fluxbox.log 2>&1 &
    x11vnc -display :99 -nopw -forever -shared -rfbport 5900 >/tmp/x11vnc.log 2>&1 &
    websockify --web /usr/share/novnc 6080 localhost:5900 >/tmp/websockify.log 2>&1 &

    gxodus auth "$CONFIG_ARG" "$CONFIG_VAL"
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
        echo "After logging in via noVNC, the export will continue automatically."
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
