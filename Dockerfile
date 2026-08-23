# Builds ssoosshd for the operator-facing Docker Compose deployment
# (deploy/docker-compose.yml). Built and pushed to ghcr.io/mnestor/ssoosshd
# by the build-image job in .github/workflows/build.yaml, so an operator
# running the compose file pulls a published image and never builds this
# themselves -- `docker compose up` alone is enough.
#
# Separate from .goreleaser.yml, which cross-compiles client/server/PAM as
# bare binaries (not container images) and does its own frontend build via
# `make frontend` as an earlier workflow step -- that pipeline ships
# .deb/.rpm/.zip artifacts for the client and PAM module, which run on the
# machine doing the sshing/sudoing, not in a container. Only the server is
# built as an image here.

# ---- frontend ---------------------------------------------------------
# server/frontend embeds server/frontend/dist via //go:embed; nothing in
# dist/ is tracked, so this is a hard prerequisite of the go build stage
# below, same as `make frontend`.
FROM node:22-alpine AS frontend
WORKDIR /src
COPY frontend/package.json frontend/pnpm-lock.yaml frontend/pnpm-workspace.yaml ./frontend/
RUN corepack enable && cd frontend && CI=true pnpm install --frozen-lockfile
COPY frontend/ ./frontend/
RUN cd frontend && CI=true pnpm build

# ---- go build -----------------------------------------------------------
FROM golang:1.26 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=frontend /src/server/frontend/dist ./server/frontend/dist
# server build stage: cgo needed for the PKCS#11 (HSM) CA key support
RUN CGO_ENABLED=1 go build -tags=nomsgpack -o /out/ssoosshd ./cmd/ssoosshd

# ---- runtime --------------------------------------------------------------
# base-debian12 (not static-) because ssoosshd is now dynamically linked
# (cgo, dlopen for PKCS#11 modules). To use an HSM in-container, mount the
# PKCS#11 module and its deps into the image (see docs/hsm.md).
FROM gcr.io/distroless/base-debian12:nonroot
COPY --from=build /out/ssoosshd /usr/local/sbin/ssoosshd
USER nonroot:nonroot
EXPOSE 8080
ENTRYPOINT ["/usr/local/sbin/ssoosshd"]
CMD ["-c", "/etc/ssoossh/ssoosshd.yaml"]
