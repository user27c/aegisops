# fault-lab 必须从受版本控制的 Go 源码构建；不依赖本地未跟踪的 bin/ 产物。
FROM golang:1.25.3-alpine AS builder
WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download
COPY cmd/ cmd/
COPY internal/ internal/
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" \
    -o /out/faultlab-server ./cmd/server

FROM alpine:3.19
RUN apk add --no-cache ca-certificates tzdata \
    && addgroup -S -g 65532 aegisops \
    && adduser -S -D -H -u 65532 -G aegisops aegisops
COPY --from=builder --chown=65532:65532 /out/faultlab-server /usr/local/bin/faultlab-server
USER 65532:65532
ENV PORT=:8080
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/faultlab-server"]
