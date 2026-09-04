# syntax=docker/dockerfile:1
FROM golang:1.26-alpine AS builder
WORKDIR /src
RUN apk add --no-cache ca-certificates git
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG APP=velis-api
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/velis ./cmd/${APP}

FROM alpine:3.23
RUN apk add --no-cache ca-certificates tzdata && addgroup -S velis && adduser -S -G velis velis
WORKDIR /app
COPY --from=builder /out/velis /usr/local/bin/velis
COPY --from=builder /src/migrations /app/migrations
USER velis
ENTRYPOINT ["/usr/local/bin/velis"]
