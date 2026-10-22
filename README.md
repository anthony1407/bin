# bin

**bin** is a single-node, S3-compatible object storage server with a built-in
web console. It ships as one static binary (or a small distroless image) that
serves two ports:

| Port | What |
|------|------|
| `9000` | S3 API: works with aws-cli, rclone and the AWS SDKs |
| `9001` | Web console and its admin API |

## Features

- **S3 API**: buckets, objects, `ListObjects` V1/V2, `CopyObject`,
  `DeleteObjects`, multipart uploads, range reads, presigned URLs, and
  checksums (CRC32, CRC32C, CRC64NVME, SHA-1, SHA-256).
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
- **Access keys**: per-app credentials limited to chosen buckets, optionally
  read-only, with an expiry date. Secrets are encrypted at rest.

## Quick start

### With Docker Compose

1. Create a `.env` file next to `docker-compose.yml`:

   ```env
   BIN_ROOT_USER=admin
   BIN_ROOT_PASSWORD=change-me-to-something-long
   ```

2. Build and start:

   ```sh
   docker compose up -d --build
   ```

3. Open the console at <http://localhost:9001> and sign in with the values from
   `.env`. Point S3 clients at <http://localhost:9000>.

To publish other host ports, add `BIN_S3_PORT` and `BIN_CONSOLE_PORT` to `.env`.

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

Run a bin server, then start the Vite dev server, which proxies `/api` to it:

```sh
cd web
npm install
npm run css:watch   # Tailwind, in one terminal
npm run dev         # Vite on http://localhost:5173, in another
```

Set `BIN_CONSOLE_URL` if the console is not at `http://localhost:9001`.

### Tests

```sh
make test         # Go unit and end-to-end tests (no Docker needed)
make vet          # go vet and gofmt
make integration  # aws-cli, rclone and curl against the image (needs Docker)
make crash        # SIGKILL durability tests (needs Docker)
make check        # all of the above
```

## Scope and limits

bin targets single-node deployments: one process, one disk, no replication or
erasure coding. Not supported yet:

- **Versioning, lifecycle rules, object lock, event webhooks**: planned. The
  storage schema is already ready for versions.
- **Replication, KMS encryption, IAM policies and audit**: out of scope.

## Documentation

- [docs/admin-api.md](docs/admin-api.md): the console's admin API.
