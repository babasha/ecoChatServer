# Build stage
FROM golang:1.23-alpine AS builder

WORKDIR /app

# Copy go mod files
COPY ecochatserver/go.mod ecochatserver/go.sum ./
RUN go mod download

# Copy source code
COPY ecochatserver/ ./

# Build the application
RUN go build -o bin/ecochatserver .

# Runtime stage
FROM alpine:latest

WORKDIR /app

# Install ca-certificates for HTTPS
RUN apk --no-cache add ca-certificates

# Copy binary from builder
COPY --from=builder /app/bin/ecochatserver .

# Expose port (Railway will override this with PORT env var)
EXPOSE 8080

# Run the application
CMD ["./ecochatserver"]
