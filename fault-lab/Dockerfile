# fault-lab 故障应用镜像：Go + distroless，非 root，OOM 通过受控分配实现。
FROM golang:1.25.3-bookworm AS builder
WORKDIR /src
COPY fault-lab/go.mod fault-lab/go.sum ./
RUN go mod download
COPY fault-lab/ ./
ARG VERSION=dev
ARG COMMIT=unknown
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -buildvcs=true \
    -ldflags "-X main.version=${VERSION} -X main.commit=${COMMIT}" \
    -o /out/fault-lab ./cmd/server

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=builder /out/fault-lab /fault-lab
USER nonroot:nonroot
ENTRYPOINT ["/fault-lab"]
EXPOSE 8080
