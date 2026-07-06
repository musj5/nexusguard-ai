# Copyright 2024 Mustafa Al-Aqrawi (Smile Spoon). All rights reserved.
# Use of this source code is governed by the MIT License.

# Build stage
FROM golang:1.22-alpine AS builder

LABEL maintainer="Mustafa Al-Aqrawi (Smile Spoon)"
LABEL description="NexusGuard AI - Intelligent Reverse Proxy for AI Applications"

WORKDIR /app

# Install dependencies
RUN apk add --no-cache git

# Copy go mod files
COPY go.mod go.sum ./
RUN go mod download

# Copy source code
COPY . .

# Build the binary
RUN CGO_ENABLED=0 GOOS=linux go build -a -installsuffix cgo \
    -ldflags="-s -w -X main.version=$(git describe --tags --always 2>/dev/null || echo 'docker')" \
    -o nexusguard ./cmd/nexusguard

# Runtime stage
FROM alpine:latest

RUN apk --no-cache add ca-certificates

WORKDIR /root/

# Copy binary from builder
COPY --from=builder /app/nexusguard .

# Expose proxy port
EXPOSE 8080

# Run the proxy
ENTRYPOINT ["./nexusguard"]
CMD ["--daemon", "--port", "8080"]
