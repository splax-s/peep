# syntax=docker/dockerfile:1.6

FROM golang:1.24-alpine AS builder
WORKDIR /app
RUN apk add --no-cache build-base ca-certificates git
COPY api/go.mod api/go.sum ./
COPY pkg/ ../pkg
RUN go mod download
COPY api/ ./
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o /out/api ./cmd/api
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go install github.com/pressly/goose/v3/cmd/goose@v3.26.0

FROM alpine:3.20
RUN addgroup -S peep && adduser -S peep -G peep
RUN apk add --no-cache ca-certificates tzdata
WORKDIR /home/peep
COPY --from=builder /out/api /usr/local/bin/api
COPY --from=builder /go/bin/goose /usr/local/bin/goose
COPY db/migrations ./db/migrations
ENV DB_MIGRATIONS_DIR=/home/peep/db/migrations
USER peep
EXPOSE 4000
ENTRYPOINT ["/usr/local/bin/api"]
