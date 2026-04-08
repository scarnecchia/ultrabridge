FROM golang:1.25-alpine AS builder

WORKDIR /build
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /ultrabridge ./cmd/ultrabridge/
RUN CGO_ENABLED=0 go build -o /ub-mcp ./cmd/ub-mcp/

FROM alpine:3.20 AS ub-mcp

RUN apk add --no-cache ca-certificates tzdata
COPY --from=builder /ub-mcp /usr/local/bin/ub-mcp

EXPOSE 8081
ENTRYPOINT ["ub-mcp"]

FROM alpine:3.20 AS ultrabridge

RUN apk add --no-cache ca-certificates tzdata poppler-utils
COPY --from=builder /ultrabridge /usr/local/bin/ultrabridge

EXPOSE 8443
ENTRYPOINT ["ultrabridge"]
