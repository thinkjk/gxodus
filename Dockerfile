FROM golang:1.23-alpine AS builder

WORKDIR /build
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -ldflags "-s -w" -o gxodus ./cmd/gxodus/

FROM chromedp/headless-shell:latest

# Install noVNC dependencies for interactive auth
RUN apt-get update && apt-get install -y --no-install-recommends \
    novnc websockify x11vnc xvfb fluxbox \
    && rm -rf /var/lib/apt/lists/*

COPY --from=builder /build/gxodus /usr/local/bin/gxodus
COPY docker-entrypoint.sh /usr/local/bin/docker-entrypoint.sh
RUN chmod +x /usr/local/bin/docker-entrypoint.sh

ENV DISPLAY=:99
ENV GXODUS_CONFIG_DIR=/config
ENV GXODUS_OUTPUT_DIR=/exports

EXPOSE 6080

VOLUME ["/config", "/exports"]

ENTRYPOINT ["docker-entrypoint.sh"]
CMD ["export"]
