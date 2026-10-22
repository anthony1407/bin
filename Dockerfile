# syntax=docker/dockerfile:1

# --- Web panel -----------------------------------------------------------------
# Node is only needed here; the result is embedded in the Go binary.
FROM node:24-alpine AS web-deps
WORKDIR /src/web
COPY web/package.json web/package-lock.json ./
RUN --mount=type=cache,target=/root/.npm npm ci --no-audit --no-fund

FROM web-deps AS web
COPY web/ ./
RUN npm run build

# Live-reloading panel for docker-compose.dev.yml; not part of the image below.
FROM web-deps AS web-dev
COPY web/ ./
EXPOSE 5173
CMD ["npm", "run", "dev", "--", "--host", "0.0.0.0", "--port", "5173", "--strictPort"]

# --- Go build ------------------------------------------------------------------
FROM golang:1.27-alpine AS build
WORKDIR /src
COPY go.mod go.sum* ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY . .
COPY --from=web /src/internal/webui/dist ./internal/webui/dist
# Stamped into "bin --version"; the Makefile passes them from git.
ARG VERSION=dev
ARG COMMIT=
ARG DATE=
# The cache mounts keep compiled packages between builds, so a rebuild after
# a small change takes seconds.
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 go build -trimpath -buildvcs=false \
      -ldflags "-s -w -X bin/internal/buildinfo.Version=${VERSION} -X bin/internal/buildinfo.Commit=${COMMIT} -X bin/internal/buildinfo.Date=${DATE}" \
      -o /out/bin ./cmd/bin \
 && mkdir -p /out/data

# --- Runtime -------------------------------------------------------------------
# Static binary on distroless: no shell, no package manager, non-root user.
# "bin" is on the PATH, so `docker exec <container> bin --version` works.
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/bin /usr/local/bin/bin
COPY --from=build --chown=65532:65532 /out/data /data
ENV BIN_DATA_DIR=/data
VOLUME /data
EXPOSE 9000 9001
HEALTHCHECK --interval=30s --timeout=5s --start-period=5s CMD ["bin", "healthcheck"]
# Arguments replace "server": `docker run bin --help`, `docker run bin --version`.
ENTRYPOINT ["bin"]
CMD ["server"]
