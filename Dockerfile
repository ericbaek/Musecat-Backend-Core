# syntax=docker/dockerfile:1

#########################
# Build the application #
#########################
FROM golang:1.24-alpine AS build

WORKDIR /src

# PocketBase uses modernc.org/sqlite, which needs CGO and a compiler toolchain.
RUN apk add --no-cache build-base

ENV CGO_ENABLED=1 \
    GOOS=linux \
    GOARCH=amd64 \
    GOTOOLCHAIN=auto

COPY go.mod go.sum ./
RUN go mod download

COPY . .

# Build the PocketBase extension binary from the repository root.
RUN go build -o /out/pocketbase-server .


#########################
# Runtime image         #
#########################
FROM alpine:3.20

RUN apk add --no-cache ca-certificates tzdata wget

WORKDIR /app

# Copy the compiled binary and runtime assets needed at runtime.
COPY --from=build /out/pocketbase-server /app/pocketbase-server
# COPY migrations ./migrations
COPY geo ./geo
COPY docs ./docs
COPY docs-site ./docs-site

# Provide a dedicated data directory; mount a volume to persist instance data.
ENV PB_DATA_DIR=/pb_data
RUN mkdir -p "${PB_DATA_DIR}" && chown -R nobody:nogroup "${PB_DATA_DIR}"
VOLUME ["/pb_data"]

EXPOSE 8090

HEALTHCHECK --interval=10s --timeout=3s --start-period=5s --retries=3 CMD \
  wget -qO- http://127.0.0.1:8090/hello || exit 1

ENTRYPOINT ["/app/pocketbase-server"]
CMD ["serve", "--http=0.0.0.0:8090"]
