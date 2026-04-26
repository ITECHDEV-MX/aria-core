FROM golang:1.25-alpine AS builder

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o /out/aria-core ./cmd/aria-core

FROM alpine:3.21
RUN adduser -D -u 10001 ariacore
USER ariacore
WORKDIR /home/ariacore
COPY --from=builder /out/aria-core /usr/local/bin/aria-core

EXPOSE 18080
ENTRYPOINT ["aria-core"]
