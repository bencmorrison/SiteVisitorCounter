FROM golang:1.24-alpine AS builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-w -s" -o site-counter .

FROM alpine:3.21
RUN adduser -D -H appuser
WORKDIR /app
COPY --from=builder /app/site-counter .
USER appuser
EXPOSE 8080
HEALTHCHECK --interval=30s --timeout=5s --start-period=5s --retries=3 \
  CMD wget -qO- http://localhost:8080/counter/health || exit 1
CMD ["./site-counter"]
