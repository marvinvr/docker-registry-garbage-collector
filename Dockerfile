ARG GO_IMAGE=golang:1.24-alpine
ARG REGISTRY_IMAGE=registry:3
ARG RUNTIME_IMAGE=alpine:3.22

FROM ${GO_IMAGE} AS build
WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .
ARG TARGETOS=linux
ARG TARGETARCH
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -ldflags="-s -w" -o /out/registry-gc ./cmd/registry-gc

FROM ${REGISTRY_IMAGE} AS registry-bin

FROM ${RUNTIME_IMAGE}
RUN apk add --no-cache ca-certificates tzdata
COPY --from=registry-bin /bin/registry /bin/registry
COPY --from=build /out/registry-gc /usr/local/bin/registry-gc

ENTRYPOINT ["/usr/local/bin/registry-gc"]
