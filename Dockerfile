FROM node:24-alpine AS web
WORKDIR /src/web
COPY web/package*.json ./
RUN npm ci
COPY web/ ./
RUN npm run build

FROM golang:1.26-alpine AS go-build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd/ ./cmd/
COPY internal/ ./internal/
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /gateway ./cmd/server

FROM alpine:3.22
RUN addgroup -S gateway && adduser -S gateway -G gateway && mkdir -p /data /strm /app/web/dist && chown -R gateway:gateway /data /strm /app
WORKDIR /app
COPY --from=go-build /gateway /app/gateway
COPY --from=web /src/web/dist /app/web/dist
USER gateway
ENV DATA_DIR=/data WEB_DIST=/app/web/dist HTTP_ADDR=:8080
EXPOSE 8080
VOLUME ["/data"]
ENTRYPOINT ["/app/gateway"]
