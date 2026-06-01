ARG GO_IMAGE=cgr.dev/chainguard/go:latest
ARG RUNTIME_IMAGE=ghcr.io/distribution/distribution:3

FROM ${GO_IMAGE} AS build
WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .
ARG TARGETOS=linux
ARG TARGETARCH
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -ldflags="-s -w" -o /out/registry-gc ./cmd/registry-gc

FROM ${RUNTIME_IMAGE}
COPY --from=build /out/registry-gc /usr/local/bin/registry-gc

ENTRYPOINT ["/usr/local/bin/registry-gc"]
