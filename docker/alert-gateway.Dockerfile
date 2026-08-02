# aegisops-alert-gateway 镜像：两阶段构建，distroless 运行，不含 shell/curl。
FROM golang:1.25.3-bookworm AS builder
WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY api/ api/
COPY cmd/ cmd/
COPY internal/ internal/

ARG VERSION=dev
ARG COMMIT=unknown
ARG CREATED=unknown
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -buildvcs=true \
    -ldflags "-X main.version=${VERSION} -X main.commit=${COMMIT} -X main.created=${CREATED}" \
    -o /out/alert-gateway ./cmd/alert-gateway

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=builder /out/alert-gateway /alert-gateway
USER 65532:65532
ENTRYPOINT ["/alert-gateway"]
EXPOSE 8080
