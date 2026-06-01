# Docker Registry Garbage Collector Plan

## Goal

Build a small Go service that runs in a Docker container and periodically cleans old images from a Docker Registry HTTP API V2-compatible registry.

The service has no GUI. Configuration is environment-variable based.

## Non-Goals

- Do not provide a GUI.
- Do not support vendor-specific registry APIs in the first version.
- Do not delete arbitrary blobs directly through custom filesystem logic.
- Do not run unsafe garbage collection while the registry can receive writes.

## Required Registry Access

This project is intended to actually reclaim registry storage, not only delete manifest references.

Therefore the container must have:

- Network access to the registry API.
- Credentials with permission to delete manifests.
- Access to the registry configuration file.
- Access to the registry storage backend used by that configuration.

For filesystem-backed registries, mount the registry data volume into the cleaner container.

For object-storage-backed registries, provide the same registry config and storage credentials that the registry uses.

The cleaner must use the official registry garbage collection behavior. It must not reimplement blob sweeping manually.

## Registry Requirements

The target registry must be Docker/CNCF Distribution-compatible.

Required API behavior:

- `GET /v2/` works.
- `GET /v2/_catalog` is available, or repositories are provided explicitly.
- `GET /v2/<repo>/tags/list` works.
- `HEAD` or `GET /v2/<repo>/manifests/<tag>` returns `Docker-Content-Digest`.
- `DELETE /v2/<repo>/manifests/<digest>` is enabled.

Registry deletion must be enabled:

```yaml
storage:
  delete:
    enabled: true
```

Garbage collection requires the registry to be read-only or stopped while GC runs.

## Configuration

Environment variables:

- `REGISTRY_URL`: required, example `https://registry.example.com`
- `REGISTRY_USERNAME`: optional
- `REGISTRY_PASSWORD`: optional
- `REGISTRY_TOKEN`: optional bearer token alternative
- `CRON_SCHEDULE`: required, example `0 3 * * *`
- `THRESHOLD_DAYS`: required, example `30`
- `MIN_IMAGES_KEEP`: required, default suggestion `3`
- `PROTECTED_TAGS`: optional comma-separated list; additive only
- `DRY_RUN`: optional boolean, default `true`
- `REPOSITORIES`: optional comma-separated repo list; if empty, use registry catalog
- `PAGE_SIZE`: optional, default `100`
- `RUN_ON_START`: optional boolean, default `true`
- `LOG_LEVEL`: optional, default `info`
- `REGISTRY_CONFIG_PATH`: required for real GC, example `/etc/docker/registry/config.yml`
- `RUN_GARBAGE_COLLECT`: optional boolean, default `false`
- `GARBAGE_COLLECT_DRY_RUN`: optional boolean, default follows `DRY_RUN`
- `GARBAGE_COLLECT_DELETE_UNTAGGED`: optional boolean, default `true`

`latest` is always a protected tag. It cannot be removed from the protected tag set through configuration.

The default policy assumes tag-based retention. Untagged manifests are disposable unless still reachable from retained tagged manifests.

## Deletion Rules

For each repository:

1. List tags.
2. Resolve each tag to its manifest digest.
3. Fetch manifest/config metadata to determine image creation time.
4. Group tags by manifest digest.
5. Protect any digest referenced by:
   - `latest`
   - any tag in `PROTECTED_TAGS`
6. Sort remaining digests by created time, newest first.
7. Keep at least `MIN_IMAGES_KEEP` digests per repository.
8. Delete only digests that are:
   - older than `THRESHOLD_DAYS`
   - not protected
   - outside the minimum keep set

If a protected tag points to a digest, the whole digest is protected. Never delete that digest through another tag.

Age is based on the image config `created` timestamp. Generic Registry V2 does not expose a portable tag push timestamp.

## Garbage Collection

After manifest deletion, optionally run registry garbage collection.

The cleaner should call the official registry GC command, using the configured registry config path:

```sh
registry garbage-collect /etc/docker/registry/config.yml
```

In dry-run mode:

```sh
registry garbage-collect --dry-run /etc/docker/registry/config.yml
```

If `GARBAGE_COLLECT_DELETE_UNTAGGED=true`, include:

```sh
--delete-untagged
```

This should default to `true` because old manifests can become untagged after mutable tags are repushed. Keeping untagged manifests by default would make storage cleanup less effective.

Garbage collection must not run unless the operator has ensured that the registry is read-only or stopped.

The Docker image for this project may include the official `registry` binary for GC execution. The application logic itself remains Go code.

## Go Structure

Suggested packages:

- `cmd/registry-gc/main.go`: entrypoint, config load, scheduler setup
- `internal/config`: env parsing and validation
- `internal/registry`: Docker Registry V2 client
- `internal/planner`: deletion candidate calculation
- `internal/runner`: scheduled job execution
- `internal/gc`: official registry garbage-collect execution
- `internal/logging`: structured logger setup

Suggested dependencies:

- Standard `net/http`
- `github.com/robfig/cron/v3` for cron scheduling
- `log/slog` for logging

## Output

Each run should log:

- registry URL
- dry-run status
- repositories scanned
- tags inspected
- protected digests
- kept digests
- deletion candidates
- successful manifest deletes
- skipped deletes with reasons
- whether garbage collection ran
- garbage collection dry-run status
- API or GC failures
