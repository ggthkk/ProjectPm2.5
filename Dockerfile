FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY *.go ./
RUN CGO_ENABLED=0 go test ./... && CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/discord-digest .

FROM alpine:3.23
RUN apk add --no-cache ca-certificates && addgroup -g 10001 bot && adduser -D -u 10001 -G bot bot && mkdir /data && chown bot:bot /data
COPY --from=build /out/discord-digest /usr/local/bin/discord-digest
USER 10001:10001
ENV DATA_DIR=/data TZ=Asia/Bangkok GOMEMLIMIT=96MiB
WORKDIR /data
ENTRYPOINT ["discord-digest"]
CMD ["-mode=daemon"]
HEALTHCHECK --interval=60s --timeout=5s --start-period=180s --retries=3 CMD ["discord-digest", "-mode=health"]
