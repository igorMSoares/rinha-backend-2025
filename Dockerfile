FROM golang:1.23-alpine AS builder

WORKDIR /app

# Install git (needed for some Go modules)
RUN apk add --no-cache git

COPY go.mod go.sum ./

RUN go mod download

COPY . .

RUN CGO_ENABLED=0 GOOS=linux go build -a -installsuffix cgo -o main ./main.go

# Use a minimal base image for the final stage
FROM alpine:latest

# Install ca-certificates for HTTPS requests
# RUN apk --no-cache add ca-certificates

# Create a non-root user
RUN adduser -D -s /bin/sh appuser

WORKDIR /app/

COPY --chown=appuser:appuser --from=builder /app/main .

USER appuser

EXPOSE 8081

CMD ["./main"]

