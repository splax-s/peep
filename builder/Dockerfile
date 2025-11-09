# syntax=docker/dockerfile:1.6

FROM golang:1.24-alpine AS builder
WORKDIR /src
RUN apk add --no-cache build-base ca-certificates git
COPY builder/go.mod builder/go.sum ./builder/
COPY pkg/ ./pkg
WORKDIR /src/builder
RUN go mod download
COPY builder/ ./
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o /out/builder ./cmd/builder

FROM alpine:3.20
RUN addgroup -S peep && adduser -S peep -G peep
RUN apk add --no-cache ca-certificates tzdata git openssh
WORKDIR /home/peep
COPY --from=builder /out/builder /usr/local/bin/builder
USER peep
ENTRYPOINT ["/usr/local/bin/builder"]
