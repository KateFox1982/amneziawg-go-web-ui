FROM golang:1.26-alpine AS builder
RUN apk add --no-cache git make gcc musl-dev linux-headers

WORKDIR /build
COPY go.mod go.sum ./
COPY web-ui/go.mod web-ui/go.sum ./web-ui/
RUN --mount=type=cache,id=awg_mod,target=/go/pkg/mod \
    --mount=type=cache,id=awg_build,target=/root/.cache/go-build \
    go mod download && cd web-ui && go mod download

COPY . .

# The frontend is a Fyne application compiled to WebAssembly. "fyne package"
# emits the loader page, its stylesheets and the bundle straight into
# web-ui/wasm
RUN --mount=type=cache,id=awg_mod,target=/go/pkg/mod \
    --mount=type=cache,id=awg_build,target=/root/.cache/go-build \
    cd web-ui && go tool fyne package -os wasm --name bundle --release

RUN --mount=type=cache,id=awg_mod,target=/go/pkg/mod \
    --mount=type=cache,id=awg_build,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /app/api .

# AmneziaWG upstream versions. Pinned to explicit release tags so a rebuild
# can never silently pull a newer (or broken) master: bump these deliberately
# and rebuild. Both must be from the same AmneziaWG generation - the tools
# only know how to serialize the UAPI keys the matching engine understands.
#   amneziawg-go    https://github.com/amnezia-vpn/amneziawg-go/tags
#   amneziawg-tools https://github.com/amnezia-vpn/amneziawg-tools/tags
ARG AWG_GO_VERSION=v3.1.20260828
ARG AWG_TOOLS_VERSION=v3.1.20260812

RUN --mount=type=cache,id=awg_mod,target=/go/pkg/mod \
    --mount=type=cache,id=awg_build,target=/root/.cache/go-build \
    git clone --depth 1 --branch "$AWG_GO_VERSION" https://github.com/amnezia-vpn/amneziawg-go.git \
    && cd amneziawg-go && make && make install

RUN  --mount=type=cache,id=awg_mod,target=/go/pkg/mod \
    --mount=type=cache,id=awg_build,target=/root/.cache/go-build \
    git clone --depth 1 --branch "$AWG_TOOLS_VERSION" https://github.com/amnezia-vpn/amneziawg-tools.git \
    && cd amneziawg-tools/src && make && make WITH_WGQUICK=yes install

# ── Final image ───────────────────────────────────────────────────────────────
FROM alpine:3.19

RUN apk update && apk add \
    curl \
    iptables \
    iptables-legacy \
    bash \
    iproute2 \
    openresolv \
    tini \
    && rm -rf /var/cache/apk/*

RUN mkdir -p /var/log/amnezia /etc/amnezia/amneziawg

COPY --from=builder /usr/bin/amneziawg-go /usr/bin/proxy
COPY --from=builder /usr/bin/awg /usr/bin/awg
COPY --from=builder /usr/bin/awg-quick /usr/bin/awg-quick
COPY --from=builder /app/api /usr/bin/api
COPY --from=builder /build/web-ui/wasm/ /app/web-ui/wasm/

COPY scripts/ /app/scripts/
RUN chmod +x /app/scripts/*.sh

# The api binary resolves ./web-ui/wasm from here.
WORKDIR /app

ENV WEB_UI_PORT=80
# awg-quick launches the userspace WireGuard implementation via this env var
# (defaults to "amneziawg-go"); renaming the binary to "proxy" and pointing
# awg-quick at it hides "amneziawg-go" from `ps aux` output.
ENV WG_QUICK_USERSPACE_IMPLEMENTATION=proxy

# Health check
HEALTHCHECK --interval=30s --timeout=10s --start-period=5s --retries=3 \
    CMD curl -f http://localhost:$WEB_UI_PORT/status || exit 1

# tini becomes the real PID 1 in the container: it reaps any orphaned
# grandchild processes (e.g. leftover from awg-quick internals) that would
# otherwise pile up as zombies, since "api" itself only reaps its own
# direct exec.Command children, not reparented orphans.
ENTRYPOINT ["/sbin/tini", "--", "/app/scripts/start.sh"]
