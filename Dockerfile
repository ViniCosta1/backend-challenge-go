FROM golang:1.26.8-alpine AS builder

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/wager-api ./cmd/api

FROM alpine:3.24

RUN apk add --no-cache ca-certificates tzdata \
    && addgroup -S wager \
    && adduser -S -G wager wager
COPY --from=builder /out/wager-api /usr/local/bin/wager-api
USER wager
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/wager-api"]
