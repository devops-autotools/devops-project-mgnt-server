# Stage 1: Build server + agent binaries
FROM golang:alpine AS builder

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .

# Build agent binaries for common Linux architectures
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags="-w -s" \
      -o public/downloads/agent-linux-amd64 ./cmd/agent/
RUN CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -ldflags="-w -s" \
      -o public/downloads/agent-linux-arm64 ./cmd/agent/

# Build server binary (current arch — Alpine builder is amd64)
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-w -s" -o mgnt-server .

# Stage 2: Minimal production container
FROM alpine:3.19

RUN apk add --no-cache ca-certificates tzdata bash curl

# Non-root user with UID 1000 (matches typical Ubuntu host user for volume permissions)
RUN addgroup -g 1000 -S mgnt && adduser -u 1000 -S mgnt -G mgnt

WORKDIR /app

COPY --from=builder /app/mgnt-server .
COPY --from=builder /app/public ./public

RUN mkdir -p /app/data/tls && chown -R mgnt:mgnt /app

USER mgnt

EXPOSE 8080 8443

CMD ["./mgnt-server"]
