# The action is a self-contained Go binary. Building it inside a container keeps
# the runtime reproducible and means the GitHub runner needs neither Go nor
# Node.js to run it.
FROM golang:1.24-alpine AS build

WORKDIR /src

# The module has no third-party dependencies, so there is nothing to download
# and no go.sum to verify: copying the sources is enough.
COPY . .

# VERSION is informational only; it is reported in the Notploy User-Agent.
ARG VERSION=dev

# No GOOS/GOARCH is forced: the binary is built for the platform Docker is
# building for, which is the runner's platform (linux/amd64 by default).
RUN CGO_ENABLED=0 go build \
    -trimpath \
    -ldflags="-s -w -X github.com/skygenesisenterprise/notploy-actions/internal/version.Version=${VERSION}" \
    -o /out/notploy-action ./cmd/notploy-action

FROM alpine:3.21

# Needed to verify the Notploy TLS certificate.
RUN apk add --no-cache ca-certificates

COPY --from=build /out/notploy-action /usr/local/bin/notploy-action

ENTRYPOINT ["/usr/local/bin/notploy-action"]
