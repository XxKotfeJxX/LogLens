FROM golang:1.26.5-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/loglens ./cmd/loglens

FROM alpine:3.23
RUN addgroup -S loglens && adduser -S -G loglens loglens \
    && mkdir -p /data/sources /data/temp /data/exports \
    && chown -R loglens:loglens /data
COPY --from=build /out/loglens /usr/local/bin/loglens
USER loglens
ENV HTTP_HOST=0.0.0.0 \
    HTTP_PORT=8080 \
    DATABASE_PATH=/data/loglens.db \
    SOURCE_STORAGE_PATH=/data/sources \
    TEMP_STORAGE_PATH=/data/temp \
    EXPORT_STORAGE_PATH=/data/exports \
    LOG_LEVEL=info
VOLUME ["/data"]
EXPOSE 8080
ENTRYPOINT ["loglens"]

