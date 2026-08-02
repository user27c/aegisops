# aegisops-incident-api 镜像：三阶段（Web 构建 + Go 编译 + distroless）。
# Stage 1: Web 静态资源（本地 pnpm build 产物，避免容器内网络依赖）
# Stage 2: Go 编译
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
    -o /out/incident-api ./cmd/incident-api

# Stage 3: 运行
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=builder /out/incident-api /incident-api
COPY web/dist /srv/web
ENV WEB_DIST_DIR=/srv/web
USER 65532:65532
ENTRYPOINT ["/incident-api"]
EXPOSE 8080
