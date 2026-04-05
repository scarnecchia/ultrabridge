FROM golang:1.24-alpine AS builder

WORKDIR /build
COPY go.mod go.sum ./
RUN go mod tidy
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /ultrabridge ./cmd/ultrabridge/

FROM alpine:3.20

RUN apk add --no-cache ca-certificates tzdata
COPY --from=builder /ultrabridge /usr/local/bin/ultrabridge

EXPOSE 8443
ENTRYPOINT ["ultrabridge"]
