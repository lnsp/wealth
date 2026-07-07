# --- Build stage: frontend ---
FROM --platform=$BUILDPLATFORM node:20-alpine AS frontend
WORKDIR /app/frontend
COPY frontend/package.json frontend/package-lock.json ./
RUN npm ci --include=dev
COPY frontend/ ./
RUN npm run build

# --- Build stage: backend ---
FROM --platform=$BUILDPLATFORM golang:1.25-alpine AS backend
ARG TARGETOS
ARG TARGETARCH
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=frontend /app/frontend/dist ./frontend/dist
ARG BUILD_COMMIT=unknown
ARG BUILD_TIME=unknown
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build \
    -ldflags "-X main.buildCommit=$BUILD_COMMIT -X 'main.buildTime=$BUILD_TIME'" \
    -o /finance-tracker ./cmd/server

# --- Runtime stage ---
FROM alpine:3.21
RUN apk add --no-cache postgresql16-client tzdata ca-certificates su-exec
COPY --from=backend /finance-tracker /usr/local/bin/finance-tracker
RUN adduser -D -H appuser && mkdir -p /backups

# Entrypoint fixes volume ownership then drops to appuser
COPY entrypoint.sh /entrypoint.sh
RUN chmod +x /entrypoint.sh
EXPOSE 8443
ENTRYPOINT ["/entrypoint.sh"]
