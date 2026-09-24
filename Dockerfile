# syntax=docker/dockerfile:1

FROM --platform=$BUILDPLATFORM golang:1.25-bookworm AS build

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download

COPY cmd/ ./cmd/
COPY internal/ ./internal/

ARG TARGETOS
ARG TARGETARCH
ARG VERSION=0.1.0
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -ldflags="-s -w -X main.version=${VERSION}" \
    -o /out/echoview ./cmd/echoview

FROM gcr.io/distroless/static-debian13:nonroot

WORKDIR /app
ENV XDG_CONFIG_HOME=/config
COPY --from=build /out/echoview /app/echoview
COPY catalog/echonet_lite_catalog.yaml /app/catalog/
COPY examples/profiles/ /app/catalog/

EXPOSE 13610/tcp
ENTRYPOINT ["/app/echoview"]
