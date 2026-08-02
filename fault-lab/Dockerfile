FROM alpine:3.19
RUN apk add --no-cache ca-certificates tzdata
COPY bin/faultlab-server /usr/local/bin/faultlab-server
ENV PORT=:8080
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/faultlab-server"]
