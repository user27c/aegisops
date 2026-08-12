# aegisops-operator 镜像：两阶段构建，distroless 运行。
# Stage 1: 编译
FROM golang:1.26.5-bookworm AS builder
WORKDIR /src

# 先复制依赖清单，最大化 BuildKit 缓存命中
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
    -o /out/operator ./cmd/operator

# Stage 2: 运行
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=builder /out/operator /operator
USER 65532:65532
ENTRYPOINT ["/operator"]
EXPOSE 8080 8081
