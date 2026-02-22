# Stage 1: Build the Go application
FROM golang:1.24-alpine AS builder

WORKDIR /build

# Copy go mod files
COPY go.mod go.sum ./

# Download dependencies
RUN go mod download

# Copy source code
COPY *.go ./

# Build the application
RUN CGO_ENABLED=0 GOOS=linux go build -a -installsuffix cgo -o gin-git-server .

# Stage 2: Create minimal runtime image
FROM alpine:latest

# The container listens on port 8080
EXPOSE 8080

# We need the following:
# - git-daemon, because that gets us the git-http-backend CGI script
RUN apk add --update git-daemon && \
    rm -rf /var/cache/apk/*

# Copy the built binary from builder stage
COPY --from=builder /build/gin-git-server /usr/local/bin/gin-git-server

# Create git repository directory and copy repository
RUN mkdir -p /data/repos
COPY repotemplate /data/repos

# Run the application
CMD ["/usr/local/bin/gin-git-server"]
