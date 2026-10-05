# One image for the three Go programs: /manager (the controller), /httpd and
# /nfsd. Each Deployment picks its program with an explicit command; the
# default entrypoint stays /manager for kustomize (config/) and plain runs.
FROM --platform=${BUILDPLATFORM} golang:1.27.1@sha256:e0174e51e81218523251d85d248a90d24c3d5e81543b4f07a5d66229397db190 AS builder
ARG TARGETOS
ARG TARGETARCH

WORKDIR /workspace
# Copy the Go Modules manifests
COPY go.mod go.mod
COPY go.sum go.sum
# cache deps before building and copying source so that we don't need to re-download as much
# and so that source changes don't invalidate our downloaded layer
RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download

# Copy the Go source (relies on .dockerignore to filter)
COPY . .

# Build
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build,id=go-build-${TARGETARCH} \
    export CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH} && \
    go build -o manager ./cmd/ && \
    go build -o httpd ./cmd/httpd/ && \
    go build -o nfsd ./cmd/nfsd/

# Runtime stage
# nfsd's default ports (2049 and 111) are below 1024: to use them, run the
# container as UID 0 with every capability dropped except NET_BIND_SERVICE,
# or pass --listen and --portmap-listen with unprivileged ports.
FROM gcr.io/distroless/static:nonroot@sha256:e2e927ec666bae08560abb3c55d0659eceabb657f56b6782ab500a9fc7f555e3
WORKDIR /
COPY --from=builder /workspace/manager /workspace/httpd /workspace/nfsd /
USER 65532:65532

ENTRYPOINT ["/manager"]
