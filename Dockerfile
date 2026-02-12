# Stage 1: Build the Go binary
FROM golang:1.25-bookworm AS builder

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download

COPY . .

ARG VERSION=docker
RUN BUILD_TIME=$(date +%FT%T%z) && \
    GO_VERSION=$(go version | awk '{print $3}') && \
    go build -v \
        -ldflags "-X main.version=${VERSION} -X main.buildTime=${BUILD_TIME} -X main.goVersion=${GO_VERSION}" \
        -o /picoclaw ./cmd/picoclaw

# Stage 2: Runtime image
# Using full Debian so the assistant can apt-get install packages freely.
FROM debian:bookworm

RUN apt-get update && apt-get install -y --no-install-recommends \
        ca-certificates \
        curl \
        git \
        jq \
        python3 \
        python3-pip \
        python3-venv \
        build-essential \
        sudo \
    && rm -rf /var/lib/apt/lists/*

# Install picoclaw binary to /opt so it doesn't collide with
# the /usr/local volume used to persist user-installed software.
COPY --from=builder /picoclaw /opt/picoclaw/bin/picoclaw
ENV PATH="/opt/picoclaw/bin:${PATH}"

# Copy builtin skills
COPY skills/ /opt/picoclaw/builtin-skills/

# Bootstrap: set up picoclaw home with builtin skills.
# The entrypoint script handles first-run initialization
# when volumes are mounted.
COPY docker-entrypoint.sh /opt/picoclaw/docker-entrypoint.sh
RUN chmod +x /opt/picoclaw/docker-entrypoint.sh

# The assistant's workspace and config live here
VOLUME ["/root/.picoclaw"]

# Gateway port
EXPOSE 18790

ENTRYPOINT ["/opt/picoclaw/docker-entrypoint.sh"]
CMD ["gateway"]
