FROM golang:1.27.2-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /stationos ./cmd/stationos

FROM alpine:3.21
RUN apk add --no-cache ca-certificates ffmpeg && addgroup -S stationos && adduser -S -G stationos stationos && mkdir /data && chown stationos:stationos /data
COPY --from=build /stationos /usr/local/bin/stationos
USER stationos
WORKDIR /data
EXPOSE 8080
HEALTHCHECK --interval=30s --timeout=5s --start-period=5s CMD wget -q -O /dev/null http://127.0.0.1:8080/readyz || exit 1
ENTRYPOINT ["stationos"]
CMD ["-listen", "0.0.0.0:8080", "-db", "/data/stationos.db"]
