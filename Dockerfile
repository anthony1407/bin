# syntax=docker/dockerfile:1

# --- Go build ------------------------------------------------------------------
FROM golang:1.27-alpine AS build
WORKDIR /src
COPY go.mod go.sum* ./
RUN go mod download
COPY . .
ARG VERSION=dev
RUN CGO_ENABLED=0 go build -trimpath \
      -ldflags "-s -w -X bin/internal/buildinfo.Version=${VERSION}" \
      -o /out/bin ./cmd/bin \
 && mkdir -p /out/data

# --- Runtime -------------------------------------------------------------------
# Static binary on distroless: no shell, no package manager, non-root user.
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/bin /usr/local/bin/bin
COPY --from=build --chown=65532:65532 /out/data /data
ENV BIN_DATA_DIR=/data
VOLUME /data
EXPOSE 9000 9001
HEALTHCHECK --interval=30s --timeout=5s --start-period=5s CMD ["/usr/local/bin/bin", "healthcheck"]
ENTRYPOINT ["/usr/local/bin/bin"]
