FROM --platform=$BUILDPLATFORM golang:1.26-alpine AS build

WORKDIR /build

COPY go.mod go.sum ./

RUN go mod download

COPY . .

ARG TARGETOS
ARG TARGETARCH

RUN --mount=type=cache,target=/root/.cache/go-build \
    --mount=type=cache,target=/go/pkg \
    CGO_ENABLED=0 \
    GOOS=$TARGETOS \
    GOARCH=$TARGETARCH \
    go build -o ca-global-bot github.com/topi314/ca-global-bot

FROM alpine

COPY --from=build /build/ca-global-bot /bin/ca-global-bot

ENTRYPOINT ["/bin/ca-global-bot"]

CMD ["-config", "/var/lib/config.toml"]
