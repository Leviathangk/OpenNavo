FROM --platform=$BUILDPLATFORM golang:1.27.3-alpine AS build
ARG TARGETOS
ARG TARGETARCH
ENV GOOS=${TARGETOS} GOARCH=${TARGETARCH}
WORKDIR /src
COPY . .
# The heavy commands share one binary (cmd/opennavo). Modules and the Go build
# cache are cache mounts, so neither lands in image or build-cache layers; the
# build downloads only the modules these binaries need (about 220 MB, where a
# `go mod download` layer held every module of the server's tests too).
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o /out/ ./cmd/opennavo ./cmd/backup ./cmd/storage-init ./cmd/probe && \
    for name in api worker desktop-publish migrate seed seed-e2e db-bootstrap; do ln -s opennavo /out/"$name"; done

FROM alpine:3.24 AS ops
# Backups require PostgreSQL 18 clients, not a database server or its entrypoint helpers.
RUN apk upgrade --no-cache && apk add --no-cache postgresql18-client ca-certificates
COPY third-party/ /usr/share/licenses/opennavo/
COPY --from=build /out/backup /backup
ENTRYPOINT ["/backup"]

# Rebuild the privilege helper with the supported Go toolchain while retaining
# PostgreSQL's Debian base and existing volume/collation compatibility.
FROM --platform=$BUILDPLATFORM golang:1.27.3-alpine AS gosu-build
ARG TARGETOS
ARG TARGETARCH
RUN go mod download github.com/tianon/gosu@v0.0.0-20250923190938-6456aaa0f3c8 && \
    mkdir -p /src && cp -R /go/pkg/mod/github.com/tianon/gosu@v0.0.0-20250923190938-6456aaa0f3c8/. /src/ && \
    cd /src && chmod -R u+w . && go get golang.org/x/sys@v0.44.0 && \
    CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build -trimpath -o /out/gosu . && \
    mkdir -p /notices/gosu /notices/go && cp LICENSE go.mod go.sum /notices/gosu/ && \
    cp /usr/local/go/LICENSE /notices/go/ && \
    go list -m -f '{{.Path}} {{.Dir}}' all | while read -r module directory; do \
      if [ -n "$directory" ]; then \
        mkdir -p "/notices/$module"; \
        find "$directory" -maxdepth 1 -type f \( -iname '*license*' -o -iname '*notice*' -o -iname '*copying*' \) -exec cp '{}' "/notices/$module/" \;; \
      fi; \
    done

FROM postgres:18 AS postgres
RUN apt-get update && apt-get upgrade -y && rm -rf /var/lib/apt/lists/*
COPY --from=gosu-build /out/gosu /usr/local/bin/gosu
COPY --from=gosu-build /notices/ /usr/share/licenses/gosu/

# Preserve the upstream UI and rebuild the monitor rather than shipping its Go 1.16 binary.
FROM --platform=$BUILDPLATFORM golang:1.27.3-alpine AS asynqmon-build
ARG TARGETOS
ARG TARGETARCH
WORKDIR /src
RUN go mod download -json github.com/hibiken/asynqmon@v0.7.1 > /tmp/module.json && \
    cp -R /go/pkg/mod/github.com/hibiken/asynqmon@v0.7.1/. . && chmod -R u+w . && \
    go get github.com/prometheus/client_golang@v1.11.1 golang.org/x/sys@v0.44.0 google.golang.org/protobuf@v1.36.11 && \
    CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build -trimpath -ldflags='-s -w' -o /out/asynqmon ./cmd/asynqmon
RUN mkdir -p /notices/asynqmon /notices/go && cp LICENSE go.mod go.sum /notices/asynqmon/ && \
    cp /usr/local/go/LICENSE /notices/go/ && \
    go list -m -f '{{.Path}} {{.Dir}}' all | while read -r module directory; do \
      if [ -n "$directory" ]; then \
        mkdir -p "/notices/$module"; \
        find "$directory" -maxdepth 1 -type f \( -iname '*license*' -o -iname '*notice*' -o -iname '*copying*' \) -exec cp '{}' "/notices/$module/" \;; \
      fi; \
    done

FROM gcr.io/distroless/static-debian12:nonroot AS asynqmon
COPY --from=asynqmon-build /notices/ /usr/share/licenses/asynqmon/
COPY --from=asynqmon-build /out/asynqmon /asynqmon
ENTRYPOINT ["/asynqmon"]

# Apply available OS fixes without changing service major versions.
FROM redis:8-alpine AS redis
RUN apk upgrade --no-cache

FROM registry.hub.docker.com/library/caddy:2.11.6-alpine AS caddy
RUN apk upgrade --no-cache

FROM gcr.io/distroless/static-debian12:nonroot AS server
COPY third-party/ /usr/share/licenses/opennavo/
COPY --from=build /out/ /
EXPOSE 8080 9090
ENTRYPOINT ["/api"]
