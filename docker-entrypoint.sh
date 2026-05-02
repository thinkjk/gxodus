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
echo "gxodus: interval=${GXODUS_INTERVAL:-(not set, one-shot mode)}"

# Idempotent: start Xvfb + noVNC stack if not already up.
ensure_xvfb() {
    # Verify the Xvfb PROCESS is alive, not just the socket. docker restart
    # preserves /tmp (writable layer) but kills processes, leaving an orphan
    # socket pointing to a dead Xvfb. Without the process check, ensure_xvfb
    # would short-circuit and chromium would later fail with "Missing X
    # server or $DISPLAY".
    if [ -S /tmp/.X11-unix/X99 ] && pgrep -x Xvfb >/dev/null 2>&1; then
        return 0
    fi

    echo "Starting noVNC stack..."
    echo "Access the browser at: http://<your-unraid-ip>:6080/vnc.html"

    # Sweep up any orphans from the previous container generation before
    # starting fresh — same docker-restart caveat as above.
    pkill -x x11vnc 2>/dev/null || true
    pkill -x websockify 2>/dev/null || true
    pkill -x fluxbox 2>/dev/null || true

    mkdir -p /tmp/.X11-unix && chmod 1777 /tmp/.X11-unix
    rm -f /tmp/.X11-unix/X99 /tmp/.X99-lock

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

    # fluxbox spawns fbsetbg which shows an xmessage "no wallpaper-setting
    # program found" popup on first run. Kill it before the user sees it.
    (sleep 2; pkill -f "xmessage" 2>/dev/null; true) &
}

run_auth() {
    ensure_xvfb
    gxodus auth "$CONFIG_ARG" "$CONFIG_VAL"
}

# Xvfb is needed for non-interactive export too — chromium runs non-headless
# on display :99 to share the same fingerprint as the auth chromium.
ensure_xvfb

build_export_args() {
    ARGS=""
    [ -n "$GXODUS_FILE_SIZE" ] && ARGS="$ARGS --file-size $GXODUS_FILE_SIZE"
    [ -n "$GXODUS_FILE_TYPE" ] && ARGS="$ARGS --file-type $GXODUS_FILE_TYPE"
    [ -n "$GXODUS_FREQUENCY" ] && ARGS="$ARGS --frequency $GXODUS_FREQUENCY"
    [ "$GXODUS_NO_ACTIVITY_LOGS" = "true" ] && ARGS="$ARGS --no-activity-logs"
    [ -n "$GXODUS_POLL_INTERVAL" ] && ARGS="$ARGS --poll-interval $GXODUS_POLL_INTERVAL"
    [ "$GXODUS_EXTRACT" = "true" ] && ARGS="$ARGS --extract"
    [ "$GXODUS_NO_KEEP_ZIP" = "true" ] && ARGS="$ARGS --no-keep-zip"
    echo "$ARGS"
}

# Run a single export, including auto-auth if no session exists.
# Returns the exit code of `gxodus export`.
run_export_once() {
    if [ ! -f "$SESSION_FILE" ]; then
        echo "No session found. Starting authentication first..."
        run_auth
    fi

    EXPORT_ARGS=$(build_export_args)
    set +e
    if [ -n "$GXODUS_REMOTE_CHROME" ]; then
        gxodus export --output "${GXODUS_OUTPUT_DIR:-/exports}" --remote-chrome "$GXODUS_REMOTE_CHROME" "$CONFIG_ARG" "$CONFIG_VAL" $EXPORT_ARGS
    else
        gxodus export --output "${GXODUS_OUTPUT_DIR:-/exports}" "$CONFIG_ARG" "$CONFIG_VAL" $EXPORT_ARGS
    fi
    EXIT=$?
    set -e
    return $EXIT
}

if [ "$COMMAND" = "auth" ]; then
    run_auth
    exit 0

elif [ "$COMMAND" = "status" ]; then
    if [ -n "$GXODUS_REMOTE_CHROME" ]; then
        exec gxodus status --remote-chrome "$GXODUS_REMOTE_CHROME" "$CONFIG_ARG" "$CONFIG_VAL"
    else
        exec gxodus status "$CONFIG_ARG" "$CONFIG_VAL"
    fi

elif [ "$COMMAND" = "export" ]; then
    if [ -n "$GXODUS_INTERVAL" ]; then
        # Long-running scheduled mode: export every $GXODUS_INTERVAL forever.
        # Pre-start Xvfb so noVNC re-auth is reachable at any time without
        # racing with whichever cycle needs it.
        ensure_xvfb

        # After an auth failure, wait this short interval before retrying
        # instead of the full $GXODUS_INTERVAL — otherwise a single bad
        # cycle blocks for the entire cadence (e.g. 180 days).
        AUTH_RETRY_INTERVAL="${GXODUS_AUTH_RETRY:-5m}"

        while true; do
            # Use if/else so a non-zero return from run_export_once doesn't
            # trip set -e and kill the entire loop.
            if run_export_once; then
                EXIT=0
            else
                EXIT=$?
            fi

            SLEEP_FOR="$GXODUS_INTERVAL"
            if [ "$EXIT" -eq 1 ]; then
                # gxodus export exits 1 on auth failure (notify hook fires).
                # Wipe session so next cycle re-auths via noVNC, and use the
                # short retry interval so the user can recover quickly.
                echo "Auth expired or failed. Wiping session — next cycle will re-auth via noVNC."
                rm -f "$SESSION_FILE"
                SLEEP_FOR="$AUTH_RETRY_INTERVAL"
                echo "Auth retry: will retry in $SLEEP_FOR (override with GXODUS_AUTH_RETRY) instead of $GXODUS_INTERVAL."
            elif [ "$EXIT" -ne 0 ]; then
                echo "Export failed with exit $EXIT — will retry next cycle."
            fi

            echo "Sleeping for $SLEEP_FOR until next export..."
            sleep "$SLEEP_FOR"
        done
    else
        # One-shot mode (default): run once and exit.
        if run_export_once; then
            exit 0
        else
            exit $?
        fi
    fi

else
    exec gxodus "$@"
fi
