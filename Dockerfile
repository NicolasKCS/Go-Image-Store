# STAGE 1: Build the Application
FROM golang:1.25-alpine AS builder

# Set the working directory inside the container
WORKDIR /app

# Copy the dependency files first (for caching)
COPY go.mod go.sum ./
RUN go mod download

# Copy the rest of the source code
COPY . .

# Build the binary
# -o main: name the output file "main"
RUN go build -o main .

# STAGE 2: The Final Tiny Image
FROM alpine:latest

WORKDIR /root/

# Copy only the compiled binary from the builder stage
COPY --from=builder /app/main .

# Open the port
EXPOSE 8080

# Command to run when the container starts
CMD ["./main"]