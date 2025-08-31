# Build stage
FROM golang:1.23-alpine AS builder
WORKDIR /app
COPY . .
RUN go build -o my-csi-driver ./cmd/driver/main.go

# Final image
FROM alpine:3.18
RUN apk add --no-cache e2fsprogs util-linux
WORKDIR /app
COPY --from=builder /app/my-csi-driver /app/my-csi-driver
ENTRYPOINT ["/app/my-csi-driver"]
