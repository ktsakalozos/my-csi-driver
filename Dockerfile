# Build stage
FROM golang:1.24-alpine AS builder
RUN apk add --no-cache ca-certificates git
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o my-csi-driver ./cmd/driver/main.go

# Final image
FROM alpine:3.18
RUN apk add --no-cache e2fsprogs util-linux
WORKDIR /app
COPY --from=builder /app/my-csi-driver /app/my-csi-driver
ENTRYPOINT ["/app/my-csi-driver"]
