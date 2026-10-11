# syntax=docker/dockerfile:1

# ---- Build stage ----
FROM golang:1.26-alpine AS build
WORKDIR /src

# Download modules first so this layer is cached until go.mod/go.sum change.
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download

COPY . .
# modernc.org/sqlite is pure Go, so the binary is fully static with CGO off.
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/pulseboard ./cmd/pulseboard

# Data directory for the SQLite file, created here because the runtime image has no shell.
RUN mkdir -p /out/data

# ---- Runtime stage ----
# distroless "nonroot" runs as UID/GID 65532 and ships no shell or package manager.
FROM gcr.io/distroless/static-debian12:nonroot

COPY --from=build /out/pulseboard /usr/local/bin/pulseboard
COPY --from=build --chown=65532:65532 /out/data /data

ENV PULSEBOARD_ADDR=:8080 \
    PULSEBOARD_DB=/data/pulseboard.db

USER 65532:65532
VOLUME ["/data"]
EXPOSE 8080

ENTRYPOINT ["/usr/local/bin/pulseboard"]
CMD ["serve"]
