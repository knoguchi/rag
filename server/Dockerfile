FROM golang:1.24-alpine AS builder

RUN apk add --no-cache git

WORKDIR /build

COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN CGO_ENABLED=0 GOOS=linux go build -o /ragd ./cmd/ragd

FROM gcr.io/distroless/static-debian12

COPY --from=builder /ragd /ragd
COPY --from=builder /build/internal/repository/postgres/migrations /migrations

EXPOSE 8080 9090

ENTRYPOINT ["/ragd"]
