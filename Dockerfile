# Build stage runs on the builder's native platform and cross-compiles,
# so multi-arch builds (buildx) don't need QEMU emulation for Go.
FROM --platform=$BUILDPLATFORM golang:1.26-alpine AS build
ARG TARGETOS
ARG TARGETARCH
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -ldflags="-s -w" -o /out/pingbot ./cmd/pingbot

FROM gcr.io/distroless/static:nonroot
COPY --from=build /out/pingbot /pingbot
USER nonroot:nonroot
WORKDIR /
EXPOSE 8080
ENTRYPOINT ["/pingbot", "-config", "/config.yaml"]
