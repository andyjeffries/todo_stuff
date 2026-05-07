# syntax=docker/dockerfile:1.7

# 1. Build the Tailwind stylesheet. web/static/css/app.css is gitignored, so
#    the image has to generate it from input.css here. Templates and JS files
#    are inputs to Tailwind's content scanning, so the whole web/ tree is in.
FROM node:22-alpine AS css
WORKDIR /app
COPY package.json package-lock.json ./
RUN npm ci --no-audit --no-fund --ignore-scripts
COPY tailwind.config.js ./
COPY web ./web
RUN npx @tailwindcss/cli -i web/static/css/input.css -o web/static/css/app.css --minify

# 2. Build the Go binary. mattn/go-sqlite3 needs CGO, so we install the C
#    toolchain. The binary statically embeds SQLite — we don't need
#    sqlite-dev at runtime — but we still need musl, which alpine has.
FROM golang:1.26-alpine AS build
RUN apk add --no-cache build-base
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=1 GOOS=linux \
    go build -trimpath -ldflags="-s -w" -o /out/todostuff ./cmd/todostuff

# 3. Minimal runtime. ca-certificates for outbound HTTPS to api.pushover.net,
#    tzdata so TZ env var resolves, sqlite for ad-hoc DB inspection.
FROM alpine:3.21
RUN apk add --no-cache ca-certificates tzdata sqlite \
 && addgroup -S app && adduser -S -G app -h /app app
WORKDIR /app
COPY --from=build /out/todostuff /app/todostuff
COPY --from=css   /app/web/static /app/web/static
RUN mkdir -p /data && chown -R app:app /app /data
USER app
VOLUME ["/data"]
ENV DATABASE_PATH=/data/todostuff.db \
    PORT=8080
EXPOSE 8080
HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
  CMD wget -qO- http://127.0.0.1:8080/health >/dev/null 2>&1 || exit 1
ENTRYPOINT ["/app/todostuff"]
