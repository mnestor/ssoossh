FROM ubuntu:22.04

# PAM e2e testing environment
# Installs PAM, OpenSSH, syslog-ng, and Go for running e2e tests
# against a real PAM stack.

RUN apt-get update && apt-get install -y --no-install-recommends \
    libpam0g \
    libpam0g-dev \
    openssh-server \
    openssh-client \
    sudo \
    syslog-ng \
    ca-certificates \
    build-essential \
    pkg-config \
    git \
    curl \
    # Go 1.26+ is needed; pinned in docker run via golang:1.26-alpine base or setup-go
    # For now we'll download it if needed, but the test framework will provide it
    && rm -rf /var/lib/apt/lists/*

# Enable syslog-ng for PAM logging
RUN syslog-ng-ctl reload 2>/dev/null || true

# Create test user for PAM authentication
# The pam.d stack will authenticate this account
RUN useradd -m -s /bin/bash testuser && \
    usermod -p '*' testuser

# Set up sshd for testing
RUN mkdir -p /run/sshd && \
    chmod 755 /run/sshd

WORKDIR /workspace
