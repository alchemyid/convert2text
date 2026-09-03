# Multi-stage build for minimal image size & optimal compute/memory efficiency
FROM golang:1.24-alpine AS builder

WORKDIR /app

# Install build dependencies
RUN apk add --no-cache git

# Download Go modules
COPY go.mod go.sum ./
RUN go mod download

# Copy source code
COPY . .

# Build statically linked binary with optimization flags
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o convert2text ./cmd/server/main.go

# Production stage - scratch or minimal alpine
FROM alpine:3.21

WORKDIR /app

# Add unprivileged user for security
RUN adduser -D -u 1000 appuser && \
    apk --no-cache add ca-certificates tzdata

COPY --from=builder /app/convert2text /app/convert2text

USER appuser

EXPOSE 8080

ENV PORT=8080 \
    MAX_UPLOAD_SIZE_MB=32 \
    MAX_CONCURRENT_EXTRACTIONS=8 \
    MAX_DECOMPRESSED_SIZE_MB=150 \
    EXTRACTION_TIMEOUT_SEC=60

ENTRYPOINT ["/app/convert2text"]
