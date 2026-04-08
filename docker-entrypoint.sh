#!/bin/sh
set -e

COMMAND="${1:-export}"

if [ "$COMMAND" = "auth" ] && [ -z "$GXODUS_REMOTE_CHROME" ]; then
    # Start Xvfb, fluxbox, x11vnc, and noVNC for interactive auth
    echo "Starting noVNC for interactive authentication..."
    echo "Access the browser at: http://localhost:6080/vnc.html"

    Xvfb :99 -screen 0 1280x720x24 &
    sleep 1
    fluxbox &
    x11vnc -display :99 -nopw -forever -shared -rfbport 5900 &
    websockify --web /usr/share/novnc 6080 localhost:5900 &
    sleep 1

    # Run gxodus auth with visible Chrome (not headless)
    exec gxodus auth --config "${GXODUS_CONFIG_DIR}/config.toml"
elif [ "$COMMAND" = "auth" ] && [ -n "$GXODUS_REMOTE_CHROME" ]; then
    # Use remote Chrome instance
    exec gxodus auth --remote-chrome "$GXODUS_REMOTE_CHROME" --config "${GXODUS_CONFIG_DIR}/config.toml"
elif [ "$COMMAND" = "export" ]; then
    EXTRA_ARGS=""
    if [ -n "$GXODUS_REMOTE_CHROME" ]; then
        EXTRA_ARGS="--remote-chrome $GXODUS_REMOTE_CHROME"
    fi
    exec gxodus export --output "$GXODUS_OUTPUT_DIR" --config "${GXODUS_CONFIG_DIR}/config.toml" $EXTRA_ARGS
elif [ "$COMMAND" = "status" ]; then
    exec gxodus status --config "${GXODUS_CONFIG_DIR}/config.toml"
else
    exec gxodus "$@"
fi
