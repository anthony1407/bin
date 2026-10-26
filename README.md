<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="docs/logo-dark.svg">
    <img src="docs/logo-light.svg" alt="bin" height="80">
  </picture>
</p>

# bin

**bin** is a single-node, S3-compatible object storage server with a built-in
web console. It ships as one static binary (or a small Debian slim image) that
serves two ports:

| Port | What |
|------|------|
| `9000` | S3 API: works with aws-cli, rclone and the AWS SDKs |
| `9001` | Web console and its admin API |

## Features

- **S3 API**: buckets, objects, `ListObjects` V1/V2, `CopyObject`,
  `DeleteObjects`, multipart uploads, range reads, presigned URLs, and
  checksums (CRC32, CRC32C, CRC64NVME, SHA-1, SHA-256).
- **Versioning**: per bucket, enabled or suspended as in S3. Overwrites and
  deletes keep the previous versions (deletes leave a delete marker);
  `ListObjectVersions`, reads, copies and deletes by `versionId`. In the
  console, each file's **Versions** tab downloads, compares, restores or
  removes versions, and **Deleted files** brings deleted ones back.
- **CORS**: per-bucket rules (`PutBucketCors`, or the bucket settings in the
  console) so web apps can call the S3 API from the browser, for example to
  upload with presigned URLs. Preflights are answered without credentials.
- **Authentication**: AWS Signature V4 in headers, query strings and
  `aws-chunked` streaming uploads (signed and unsigned trailers). Path-style
  requests by default; virtual-host style when `BIN_DOMAIN` is set.
- **Durable writes**: data is written to a temporary file, fsynced and renamed
  before the metadata (SQLite) commit. Every object's SHA-256 is recorded and
  checked on full downloads, and an integrity scan can re-check everything.
- **Web console**: browse, upload (drag and drop, large files in parts),
  preview, share links, folders, bucket quota, tags and anonymous access
  policies, scoped access keys, and server status. Light and dark themes, and
  it works on phones.
- **Access keys and groups**: per-app credentials limited to chosen buckets,
  optionally read-only, with an expiry date. Groups grant the same bucket
  scope to many keys at once and can be disabled in one step. Secrets are
  encrypted at rest.
- **Single sign-on**: console sign-in with OpenID Connect (Keycloak, Google
  Workspace, Microsoft Entra ID, Okta, ...) or LDAP / Active Directory, for
  members of an admin group. Configured in **Identity**; root can always sign
  in.
- **Audit log**: sign-ins, changes and refused requests, from the console and
  the S3 API, searchable in the console. Reads are not recorded; entries are
  kept for 30 days by default (configurable in **Settings**, 0 keeps them
  forever).
- **Replication**: rules that keep a bucket folder in step with another S3
  server (AWS S3, Cloudflare R2, Wasabi, Backblaze B2 or any S3-compatible
  one), pushing (offsite backup) or pulling, every minute to every day and on
  demand. Only new or changed objects are copied; deletes can be mirrored, and
  then only for objects the rule copied. Plus a one-time **Import** of whole
  buckets, with progress and cancel.
- **Server log in the console**: the latest log lines, live, with level and
  text filters, and export.
- **Share links**: from 5 minutes to 7 days, listed per file with the time
  left, and revocable one by one.
- **Dropzones**: expiring upload links for people without an account. They
  upload into one bucket folder, up to a size per file, and never overwrite
  existing files.
- **Console tools**: an S3 terminal in the browser, ready-to-paste SDK
  snippets with your endpoint and keys, and a speed test for bandwidth,
  latency and disk throughput.

## Quick start

### With Docker Compose

1. Create a `.env` file next to `docker-compose.yml` (it is ignored by git),
   starting from `.env.example`, and set your own credentials.
   Without it bin has no credentials, refuses to start and keeps restarting;
   `docker compose logs` shows why:

   ```sh
   cp .env.example .env
   ```

2. Build and start:

   ```sh
   docker compose up -d --build
   ```

3. Open the console at <http://localhost:9001> and sign in with the values from
   `.env`. Point S3 clients at <http://localhost:9000>.

To publish other host ports, add `BIN_S3_PORT` and `BIN_CONSOLE_PORT` to `.env`.

Day-to-day commands, run next to `docker-compose.yml`:

| Command | What it does |
|---|---|
| `docker compose up -d --build` | Build the image from the current code and start (or update) bin |
| `docker compose up -d` | Start bin again with the image already built |
| `docker compose down` | Stop and remove the container; your data is kept in the `bin-data` volume |
| `docker compose restart` | Restart without rebuilding (e.g. after editing `.env`) |
| `docker compose logs -f` | Follow the server log |
| `docker compose ps` | Show the container and its health |
| `docker compose down -v` | Stop **and delete all data** (the `bin-data` volume): buckets, objects and keys |

To apply code changes, run `docker compose up -d --build` again: it rebuilds
the image and replaces the container, keeping the data. If a container from a
plain `docker run` named `bin` already uses ports 9000/9001, remove it first
with `docker rm -f bin`. Both setups store data in the same `bin-data` volume,
so switching from one to the other keeps your buckets.

### With docker run

```sh
docker build -t bin:dev .
docker run -d --name bin -p 9000:9000 -p 9001:9001 -e BIN_ROOT_USER=admin -e BIN_ROOT_PASSWORD=change-me-to-something-long -v bin-data:/data bin:dev
```

The commands in this README are written on one line each, so they work as-is
in bash, PowerShell and Windows `cmd`. To split one over several lines, end
each line with `\` in bash, a backtick (`` ` ``) in PowerShell, or `^` in `cmd`.

### Signing in

There is no default account. The console login and the root S3 credentials are
the two values you start the server with:

| Console field | Variable | Also accepted |
|---|---|---|
| Access key | `BIN_ROOT_ACCESS_KEY` | `BIN_ROOT_USER`, `BIN_ACCESS_KEY` |
| Secret key | `BIN_ROOT_SECRET_KEY` | `BIN_ROOT_PASSWORD`, `BIN_SECRET_KEY` |

The secret key needs at least 8 characters; 16 or more is recommended, and bin
warns at startup when it is shorter. For apps and people, create separate access
keys in the console under **Access keys** instead of sharing the root
credentials.

## Using S3 clients

**aws-cli** (v2). Save the server as a profile once:

```sh
aws configure set aws_access_key_id admin --profile bin
aws configure set aws_secret_access_key change-me-to-something-long --profile bin
aws configure set region us-east-1 --profile bin
aws configure set endpoint_url http://localhost:9000 --profile bin
```

Then use it with `--profile bin`:

```sh
aws --profile bin s3 mb s3://photos
aws --profile bin s3 cp cat.jpg s3://photos/
aws --profile bin s3 ls s3://photos/
```

**rclone**

```sh
rclone config create bin s3 provider=Other endpoint=http://localhost:9000 access_key_id=admin secret_access_key=change-me-to-something-long region=us-east-1
rclone ls bin:photos
```

**SDKs**: set the endpoint to `http://localhost:9000`, the region to
`BIN_REGION` (default `us-east-1`), and enable path-style addressing unless
`BIN_DOMAIN` is configured.

## Configuration

Settings come from environment variables or from `KEY=VALUE` lines in
`bin.env`. `bin --help` prints the full list.

| Variable | Default | Description |
|---|---|---|
| `BIN_ROOT_ACCESS_KEY` | required | Root access key (3+ characters) |
| `BIN_ROOT_SECRET_KEY` | required | Root secret key (8+ characters, 16+ recommended) |
| `BIN_DATA_DIR` | `./data` (`/data` in the image) | Objects and metadata |
| `BIN_S3_ADDR` | `:9000` | S3 API listen address |
| `BIN_CONSOLE_ADDR` | `:9001` | Console listen address |
| `BIN_REGION` | `us-east-1` | Region reported to clients and required in signatures |
| `BIN_DOMAIN` | | Enables virtual-host style requests (`bucket.domain`) |
| `BIN_PUBLIC_URL` | | S3 URL as clients see it (e.g. `https://s3.example.com`); used in share links |
| `BIN_TRUSTED_PROXIES` | | Reverse proxies (IPs or CIDRs, comma separated) whose `X-Forwarded-For` gives the real client IP for the audit log, sign-in limits and dropzone records; see below |
| `BIN_MASTER_KEY` | generated | Key that encrypts secrets at rest; see [Backups](#backups) |
| `BIN_LOG_LEVEL` | `info` | `debug`, `info`, `warn` or `error` |
| `BIN_LOG_FORMAT` | `text` | `text` or `json` |
| `BIN_LOG_REQUESTS` | `true` | One log line per request |
| `BIN_CONFIG_FILE` | `bin.env` | Optional `KEY=VALUE` file |

The data directory and listen addresses can also be passed as arguments, which
override the environment:

```sh
bin server /data --address :9100 --console-address :9101
```

Unknown `BIN_*` variables are reported at startup with a suggestion, so typos
do not go unnoticed.

### Client IP addresses

bin records who connects (audit log, dropzone uploads) by the connection's
address. Two setups hide it:

- **Docker Desktop** (Windows, macOS) forwards published ports through its own
  network, so every client shows up as the Docker gateway (e.g. `172.21.0.1`).
  On a Linux host, published ports keep the real address.
- **A reverse proxy** (Caddy, Nginx, Traefik) shows up as the proxy.

Put a reverse proxy in front that sets `X-Forwarded-For` (Caddy and Traefik do
by default; Nginx needs `proxy_set_header X-Forwarded-For
$proxy_add_x_forwarded_for;`), and set `BIN_TRUSTED_PROXIES` to its address or
network, e.g. `BIN_TRUSTED_PROXIES=172.16.0.0/12` for a proxy on the same
Docker network. bin only reads the header from those addresses: from anyone
else it could be forged.

## Operations

| Task | Command |
|---|---|
| Version | `docker exec bin bin --version` |
| Health (used by the image's `HEALTHCHECK`) | `docker exec bin bin healthcheck` |
| Health over HTTP (for load balancers) | `GET http://<host>:9000/_health/live` |
| Logs | `docker logs bin` (startup banner with the URLs, one line per request) |

### Backups

Back up the whole data directory, **including `.bin/master.key`**. Without it,
the access keys stored in the database cannot be decrypted. Alternatively, set
`BIN_MASTER_KEY` yourself and keep it in a secrets manager. Stop the container
(or take a volume snapshot) while copying, so files and metadata match.

### Integrity

Full downloads are checked against the stored SHA-256 and abort on mismatch.
To check objects nobody has read lately, run **Settings → Integrity scan** in
the console; it lists any object that no longer matches.

## Building from source

Requirements: Go 1.27+, plus Node 24+ for the web console.

```sh
make web     # builds the console into internal/webui/dist (needs Node)
make build   # builds dist/bin with the console embedded
make image   # Docker image bin:dev, stamped with the git version
```

A binary built without `make web` still runs. The console then serves only the
admin API and logs a warning at startup.

### Developing the console

You do not need to rebuild the image for every change to the panel. Keep any
bin server running (for example the `bin` container on ports 9000/9001), then
start the Vite dev server, which proxies `/api` to it:

```sh
cd web
npm install
npm run dev
```

Open <http://localhost:5173> and sign in as usual. Every save to `web/src`
shows up in the browser at once; `npm run dev` also keeps the Tailwind CSS up
to date. If the console is not at `http://localhost:9001`, set
`BIN_CONSOLE_URL` first (`BIN_CONSOLE_URL=http://localhost:19001 npm run dev`
in bash, `$env:BIN_CONSOLE_URL="http://localhost:19001"` in PowerShell, or
`set BIN_CONSOLE_URL=http://localhost:19001` in `cmd`).

Rebuild the image (`docker build -t bin:dev .`) only when you want the
finished panel inside the binary, or when you change the Go server. For server
changes you can also skip Docker and run it directly:

```sh
go run ./cmd/bin server ./data --address :9100 --console-address :9101
```

(with `BIN_ROOT_USER` and `BIN_ROOT_PASSWORD` set in the environment or in a
`bin.env` file), and point the dev server at it with
`BIN_CONSOLE_URL=http://localhost:9101`.

### Tests

```sh
make test         # Go unit and end-to-end tests (no Docker needed)
make vet          # go vet and gofmt
make integration  # aws-cli, rclone and curl against the image (needs Docker)
make crash        # SIGKILL durability tests (needs Docker)
make check        # all of the above
```

## Scope and limits

bin targets single-node deployments: one process, one disk, no clustering or
erasure coding. Not supported yet:

- **Lifecycle rules, object lock, MFA delete, event webhooks**: planned.
  Older versions stay until deleted one by one (or with the bucket).
- **KMS encryption, IAM policies**: out of scope.
- **Replication** runs on a schedule (every minute at most), not on each
  write, and in one direction per rule; two rules pushing and pulling the
  same folder are not a two-way sync.

Some console screens are previews of planned features and are not connected
to the server yet: **Policies**, native TLS certificates in **Settings**, and
the lifecycle section of bucket settings. They are marked "coming soon" and
change nothing on the server.

## Documentation

- [docs/admin-api.md](docs/admin-api.md): the console's admin API.
- [docs/compat/README.md](docs/compat/README.md): how real S3 clients behave against other S3 servers, with the captured
  logs bin's behavior is matched against.
