# PiG as a kagent native Harness: the container image (kagent-pig adapter + fused PiG Piglet Binary + baked tools).
#
# `docker build .` in the exported tree (scripts/export-byo-ext.sh; harnesses/pig/ in byo-ext) on linux/amd64 or linux/arm64 builds it: no build
# arguments, no secrets, no files outside the directory. (components/kagent/harness/ itself lacks piglet/ and policy/, which the export adds.)
# Every input is pinned: base images by digest, Debian packages by snapshot.debian.org timestamp, tool and toolchain
# archives by sha256 (ADD --checksum), PiG by an exact commit that the build verifies, Go modules by go.sum.
# Design and measurements: handoffs/0.7.1/kagent-harness-research/image-options.md.
#
# Layers, least-changing first, so a node that holds the lower layers pulls only the top two after a release:
#   1 Debian base   2 apt packages, user 65532   3 Go toolchain   4 gh rg fd jq   5 pig (PiG with the kagent Piglet fused)   6 kagent-pig
#
# `pig piglet build` builds only for the machine it runs on, so stage `piglet` runs on the TARGET platform (a native
# arm64 runner for arm64). Everything else (the adapter, the downloads) is built or fetched on the BUILD platform.
#
# The kagent sandbox runs this image as UID 65532 with no capabilities and a durable volume at /data. Nothing
# can be installed at run time, so everything the agent needs is baked here.

ARG DEBIAN_IMAGE=debian:trixie-slim@sha256:a72b4433de1cf7d6bae0c4cee74d0547f58601a643b3375f070c51600d63b6b2
ARG GOLANG_IMAGE=golang:1.27.3-trixie@sha256:af1bfc2614ebb1dbd31af514b78740fd678850e76498762b425ecee4c5c6ea80
ARG PIG_VERSION=0.7.0
ARG PIG_COMMIT=beb56447c476da70d05d8e1e1ca9e993b367cb8b
ARG GH_VERSION=2.103.3
ARG RG_VERSION=15.2.0
ARG FD_VERSION=10.5.1
ARG JQ_VERSION=1.8.2
ARG GO_VERSION=1.27.3
ARG ADAPTER_PKG=./cmd/kagent-pig
ARG TARGETARCH

# ---- adapter: cross-compiled on the build platform, never emulated
FROM --platform=$BUILDPLATFORM ${GOLANG_IMAGE} AS adapter
ARG TARGETARCH ADAPTER_PKG
WORKDIR /src
COPY adapter/go.mod adapter/go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY adapter/ ./
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=linux GOARCH=${TARGETARCH} \
    go build -trimpath -buildvcs=false -ldflags="-s -w -buildid=" -o /out/kagent-pig ${ADAPTER_PKG}

# ---- PiG source at the release tag, verified against the commit it must be
FROM ${GOLANG_IMAGE} AS pig-src
ARG PIG_VERSION PIG_COMMIT
RUN set -eux; \
    git clone --quiet --depth 1 --branch "v${PIG_VERSION}" https://github.com/MichaelKinsy/PiG.git /pig-src; \
    test "$(git -C /pig-src rev-parse HEAD)" = "${PIG_COMMIT}"

# ---- pig: the stock PiG builds the kagent Piglet into one fused Binary. It runs `go build` itself, so this stage is native
FROM pig-src AS piglet
ARG TARGETARCH
ENV CGO_ENABLED=0
WORKDIR /pig-src
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    go build -buildvcs=false -trimpath -ldflags="-s -w -buildid=" -o /out/pig-stock ./cmd/pig
WORKDIR /build
COPY piglet/ /build/piglet/
COPY policy/ /build/piglet/extensions/kagent-policy/
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    PIG_SOURCE_ROOT=/pig-src /out/pig-stock piglet build /build/piglet/piglet.yaml \
      --format binary --builder native --no-input --targets "linux/${TARGETARCH}" --out /out/pig; \
    /out/pig version

# ---- downloads: build platform, target-arch archives, sha256 verified by BuildKit (ADD --checksum)
FROM --platform=$BUILDPLATFORM ${DEBIAN_IMAGE} AS dl-amd64
ARG GH_VERSION RG_VERSION FD_VERSION JQ_VERSION GO_VERSION
ADD --checksum=sha256:4c806978ffce1cec70853302a56d70ac6da4f19bae7adca4e1a8ab00633dd91d https://github.com/cli/cli/releases/download/v${GH_VERSION}/gh_${GH_VERSION}_linux_amd64.tar.gz /dl/gh.tgz
ADD --checksum=sha256:33e15bcf1624b25cdd2a55813a47a2f95dbe126268203e76aa6a585d1e7b149c https://github.com/BurntSushi/ripgrep/releases/download/${RG_VERSION}/ripgrep-${RG_VERSION}-x86_64-unknown-linux-musl.tar.gz /dl/rg.tgz
ADD --checksum=sha256:f6c95edb190c241df6fddc815a5bab4051444993ac5d92ddc6eec0e0dc2342c7 https://github.com/sharkdp/fd/releases/download/v${FD_VERSION}/fd-v${FD_VERSION}-x86_64-unknown-linux-musl.tar.gz /dl/fd.tgz
ADD --checksum=sha256:b1c22172dd303f3be49e935aa56aa48a8b7a46e0bc838b4997d3bb451495870f https://github.com/jqlang/jq/releases/download/jq-${JQ_VERSION}/jq-linux-amd64 /dl/jq
ADD --checksum=sha256:65efbc80fdc1fa2cd2631cad5b481777a68e8725f34e89f50fc6ac714e379b40 https://go.dev/dl/go${GO_VERSION}.linux-amd64.tar.gz /dl/go.tgz

FROM --platform=$BUILDPLATFORM ${DEBIAN_IMAGE} AS dl-arm64
ARG GH_VERSION RG_VERSION FD_VERSION JQ_VERSION GO_VERSION
ADD --checksum=sha256:ee32df2323c8847f5fac6cb6b4b69ca75828e9329a8f22f677021f7b3306ce63 https://github.com/cli/cli/releases/download/v${GH_VERSION}/gh_${GH_VERSION}_linux_arm64.tar.gz /dl/gh.tgz
ADD --checksum=sha256:800b1e7206afe799dfb5a6901f23147cfaabe0e52210538100f61e86e1740915 https://github.com/BurntSushi/ripgrep/releases/download/${RG_VERSION}/ripgrep-${RG_VERSION}-aarch64-unknown-linux-musl.tar.gz /dl/rg.tgz
ADD --checksum=sha256:64c20b9b154c9d4b36d4b7df3521de771c499c4768b4b5b045ef6b01532117be https://github.com/sharkdp/fd/releases/download/v${FD_VERSION}/fd-v${FD_VERSION}-aarch64-unknown-linux-musl.tar.gz /dl/fd.tgz
ADD --checksum=sha256:8b85c817833814ddca00a144c33705546355afccf0cf39b188f3cdb48b852309 https://github.com/jqlang/jq/releases/download/jq-${JQ_VERSION}/jq-linux-arm64 /dl/jq
ADD --checksum=sha256:a06e99309dbcbdca1a8ccd2282c3ee146e55930886e26b4f1193269587165f49 https://go.dev/dl/go${GO_VERSION}.linux-arm64.tar.gz /dl/go.tgz

FROM dl-${TARGETARCH} AS unpack
RUN set -eux; mkdir -p /out/go /out/tools; \
    tar --no-same-owner -xzf /dl/go.tgz -C /out/go --strip-components=1; \
    rm -rf /out/go/test /out/go/doc /out/go/misc /out/go/api; \
    find /out/go/src -type d -name testdata -prune -exec rm -rf {} +; \
    tar -xzf /dl/gh.tgz -C /dl --strip-components=2 --wildcards "*/bin/gh"; \
    tar -xzf /dl/rg.tgz -C /dl --strip-components=1 --wildcards "*/rg"; \
    tar -xzf /dl/fd.tgz -C /dl --strip-components=1 --wildcards "*/fd"; \
    install -m 0755 /dl/gh /dl/rg /dl/fd /dl/jq /out/tools/

# ---- runtime
FROM ${DEBIAN_IMAGE}
ARG VERSION=dev
ARG REVISION=unknown
# The snapshot timestamp is the one debian:trixie-slim records in /etc/apt/sources.list.d/debian.sources for this digest.
# snapshot.debian.org is read over http: the base image has no CA bundle yet, and apt verifies the repository signatures itself.
RUN set -eux; \
    sed -i "s#http://deb.debian.org/debian-security#http://snapshot.debian.org/archive/debian-security/20261104T212339Z#; s#http://deb.debian.org/debian#http://snapshot.debian.org/archive/debian/20261104T212339Z#" /etc/apt/sources.list.d/debian.sources; \
    apt-get update -o Acquire::Check-Valid-Until=false; \
    apt-get install -y --no-install-recommends bash ca-certificates curl git python3 python3-pip python3-venv; \
    rm -rf /var/lib/apt/lists/* /var/cache/apt/* /var/log/apt /var/log/dpkg.log /var/log/alternatives.log /var/cache/ldconfig/aux-cache; \
    groupadd -g 65532 kagent; \
    useradd -u 65532 -g 65532 -d /data -M -s /bin/bash kagent; \
    mkdir -p /data /workspace; chown 65532:65532 /data
COPY --from=unpack /out/go /usr/local/go
COPY --from=unpack /out/tools/ /usr/local/bin/
COPY --from=piglet /out/pig /usr/local/bin/pig
COPY --from=pig-src /pig-src/LICENSE /pig-src/LICENSES /usr/share/licenses/pig/
COPY LICENSE NOTICE /usr/share/licenses/pig-kagent/
COPY --from=adapter /out/kagent-pig /usr/local/bin/kagent-pig

# HOME and every cache live on the durable /data volume (kagent mounts it 0777). /tmp is memory-backed in the sandbox.
# kagent sets the TLS trust variables itself and refuses an Agent that sets them, so none are set here.
ENV HOME=/data \
    PATH=/data/.local/bin:/data/go/bin:/usr/local/go/bin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin \
    GOPATH=/data/go GOCACHE=/data/.cache/go-build GOTOOLCHAIN=local \
    PYTHONUSERBASE=/data/.local PIP_BREAK_SYSTEM_PACKAGES=1 PIP_DISABLE_PIP_VERSION_CHECK=1 \
    XDG_CACHE_HOME=/data/.cache
USER 65532:65532
WORKDIR /data
EXPOSE 80 8081
LABEL org.opencontainers.image.title="pig-kagent" \
      org.opencontainers.image.description="PiG as a kagent native Harness (independent work, not endorsed by the kagent project)" \
      org.opencontainers.image.source="https://github.com/MichaelKinsy/pigpen" \
      org.opencontainers.image.licenses="Apache-2.0 AND MIT" \
      org.opencontainers.image.version="${VERSION}" \
      org.opencontainers.image.revision="${REVISION}"
# No ENTRYPOINT or CMD: the Harness resource supplies workload.command (/usr/local/bin/kagent-pig).
