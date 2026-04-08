FROM docker.io/library/golang:1.26.2-alpine AS builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o /app/notification-service ./cmd/main.go

FROM alpine:latest
RUN apk --no-cache add ca-certificates && \
    apk upgrade --no-cache zlib
WORKDIR /root/
COPY --from=builder /app/notification-service .
EXPOSE 8080
CMD ["./notification-service"]
