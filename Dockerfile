# Stage 1: Build the Go binary
FROM golang:alpine AS builder

WORKDIR /app

COPY go.mod ./
COPY . .

RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-w -s" -o mgnt-server main.go

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
