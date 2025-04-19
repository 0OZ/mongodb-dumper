FROM golang:1.24.1-alpine AS builder

RUN apk add --no-cache git

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN CGO_ENABLED=0 GOOS=linux go build -o /go/bin/dumper ./cmd/dumper

FROM mongo:6.0-focal

WORKDIR /app

# Install any additional tools needed for MongoDB operations
RUN apt-get update && apt-get install -y --no-install-recommends \
    ca-certificates \
    && rm -rf /var/lib/apt/lists/*

# Create directory for dumps with proper permissions
RUN mkdir -p /tmp/mongodb-dumps && \
    chmod 777 /tmp/mongodb-dumps

COPY --from=builder /go/bin/dumper /usr/local/bin/dumper

VOLUME /tmp/mongodb-dumps

# Set the entrypoint with absolute path
ENTRYPOINT ["/usr/local/bin/dumper"]

