# --- Build stage: frontend ---
FROM node:20-alpine AS frontend
WORKDIR /app/frontend
COPY frontend/package.json frontend/package-lock.json ./
RUN npm ci
COPY frontend/ ./
RUN npm run build

# --- Build stage: backend ---
FROM golang:1.22-alpine AS backend
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=frontend /app/frontend/dist ./frontend/dist
RUN CGO_ENABLED=0 GOOS=linux go build -o /finance-tracker ./cmd/server

# --- Runtime stage ---
FROM alpine:3.19
RUN apk add --no-cache postgresql16-client tzdata ca-certificates
COPY --from=backend /finance-tracker /usr/local/bin/finance-tracker
RUN adduser -D -H appuser
USER appuser
EXPOSE 8443
ENTRYPOINT ["finance-tracker"]
