FROM golang:1.22-alpine AS builder
WORKDIR /app
COPY go.mod go.sum* ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o /out/els-feedback-proxy ./cmd/server

FROM alpine:3.20
WORKDIR /app
RUN addgroup -S app && adduser -S app -G app
COPY --from=builder /out/els-feedback-proxy /usr/local/bin/els-feedback-proxy
USER app
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/els-feedback-proxy"]
