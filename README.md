# Docker Registry Garbage Collector

Small Go service that removes old image manifests from a Docker/CNCF Distribution-compatible registry and can then run the official registry garbage collector to reclaim storage.

It uses only the Registry V2 API for repository/tag/manifest work. It does not touch registry storage directly. Blob cleanup is delegated to `registry garbage-collect`, which is included in the Docker image.

## Behavior

The cleaner scans repositories, groups tags by manifest digest, protects configured tags, keeps the newest images per repository, and deletes old manifest digests that fall outside the retention policy.

Image age is based on the image config `created` timestamp. `latest` is always protected. For multi-arch indexes, the newest child image timestamp is used so an index is not removed while any platform image is still new.

Garbage collection is optional. If enabled, the registry must be read-only or stopped before non-dry-run GC runs.

## Configuration

| Variable | Required | Default | Description |
| --- | --- | --- | --- |
| `REGISTRY_URL` | yes | | Registry base URL, for example `https://registry.example.com`. |
| `CRON_SCHEDULE` | yes, unless `RUN_ONCE=true` | | Five-field cron schedule. |
| `THRESHOLD_DAYS` | yes | | Delete only candidates older than this many days. |
| `MIN_IMAGES_KEEP` | yes | | Newest non-protected digests to keep per repository. |
| `DRY_RUN` | no | `false` | Log candidates without deleting manifests. |
| `RUN_ON_START` | no | `true` | Run once immediately before waiting for cron. |
| `RUN_ONCE` | no | `false` | Run once and exit. Useful for manual runs. |
| `REPOSITORIES` | no | | Comma-separated repository list. Empty means use `/v2/_catalog`. |
| `PROTECTED_TAGS` | no | | Comma-separated tags to protect. `latest` is always protected. |
| `PAGE_SIZE` | no | `100` | Registry pagination size. |
| `LOG_LEVEL` | no | `info` | `debug`, `info`, `warn`, or `error`. |
| `REGISTRY_USERNAME` / `REGISTRY_PASSWORD` | no | | Basic auth credentials. Must be set together. |
| `REGISTRY_TOKEN` | no | | Static bearer token alternative. |
| `RUN_GARBAGE_COLLECT` | no | `false` | Run official registry GC after manifest deletion. |
| `REGISTRY_CONFIG_PATH` | when GC enabled | | Registry config path inside this container. |
| `GARBAGE_COLLECT_DRY_RUN` | no | follows `DRY_RUN` | Pass `--dry-run` to `registry garbage-collect`. |
| `GARBAGE_COLLECT_DELETE_UNTAGGED` | no | `true` | Pass `--delete-untagged` to GC. |
| `REGISTRY_READ_ONLY` | for real GC | `false` | Required before non-dry-run GC can run. |

## Compose

Minimal scheduled setup:

```yaml
services:
  registry-gc:
    image: docker-registry-garbage-collector:local
    restart: unless-stopped
    environment:
      REGISTRY_URL: https://registry.example.com
      CRON_SCHEDULE: "0 3 * * *"
      THRESHOLD_DAYS: "30"
      MIN_IMAGES_KEEP: "3"
      DRY_RUN: "false"
```

To also run storage garbage collection, add this to the same service:

```yaml
environment:
  RUN_GARBAGE_COLLECT: "true"
  GARBAGE_COLLECT_DRY_RUN: "false"
  GARBAGE_COLLECT_DELETE_UNTAGGED: "true"
  REGISTRY_READ_ONLY: "true"
  REGISTRY_CONFIG_PATH: /etc/docker/registry/config.yml
volumes:
  - ./config.yml:/etc/docker/registry/config.yml:ro
  - /path/to/registry/storage:/var/lib/registry
```

The included [compose.yaml](compose.yaml) is a local template using the same variables.

## Build

```sh
docker build -t docker-registry-garbage-collector:local .
```

If Docker Hub auth or rate limits are broken locally, override the base images:

```sh
docker build \
  --build-arg GO_IMAGE=mirror.gcr.io/library/golang:1.24-alpine \
  --build-arg REGISTRY_IMAGE=mirror.gcr.io/library/registry:3 \
  --build-arg RUNTIME_IMAGE=mirror.gcr.io/library/alpine:3.22 \
  -t docker-registry-garbage-collector:local .
```

## Registry Requirements

Manifest deletion must be enabled in the registry:

```yaml
storage:
  delete:
    enabled: true
```

Put that under the existing `storage:` section in the Docker Registry config file, usually mounted into the registry container at `/etc/docker/registry/config.yml`.

Example filesystem-backed registry config:

```yaml
version: 0.1
storage:
  filesystem:
    rootdirectory: /var/lib/registry
  delete:
    enabled: true
http:
  addr: :5000
```

After changing this config, restart the registry container so manifest deletes are accepted.

For real storage reclamation, mount the registry config and the backing storage into this container, or provide the same object-storage credentials the registry uses.
