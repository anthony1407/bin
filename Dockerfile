# syntax=docker/dockerfile:1

# --- Go build ------------------------------------------------------------------
FROM golang:1.27-alpine AS build
WORKDIR /src
COPY go.mod go.sum* ./
RUN go mod download
COPY . .
# Stamped into "bin --version"; the Makefile passes them from git.
ARG VERSION=dev
ARG COMMIT=
ARG DATE=
RUN CGO_ENABLED=0 go build -trimpath -buildvcs=false \
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
