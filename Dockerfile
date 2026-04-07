FROM golang:1.25-alpine AS builder

WORKDIR /build
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /ultrabridge ./cmd/ultrabridge/

FROM alpine:3.20

RUN apk add --no-cache ca-certificates tzdata poppler-utils
COPY --from=builder /ultrabridge /usr/local/bin/ultrabridge

EXPOSE 8443
ENTRYPOINT ["ultrabridge"]
