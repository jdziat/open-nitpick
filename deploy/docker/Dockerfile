# syntax=docker/dockerfile:1
#
# nitpick-server, which hosts the notary and the hub UI. Build from the
# repository root:
#   docker build -f deploy/docker/Dockerfile -t nitpick-server .

FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
# The UI is built here, and embedded by the compile after it. A go build run
# without this step produces a server whose UI pages say the UI was not built.
RUN go run ./internal/server/uibuild
# A static binary, so the final image needs no libc.
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o /out/nitpick-server ./cmd/nitpick-server \
 && mkdir -p /out/data

# distroless/static carries CA certificates, which the server needs to fetch an
# OIDC issuer's keys over HTTPS, and no shell, which a record server does not.
FROM gcr.io/distroless/static:nonroot
COPY --from=build /out/nitpick-server /nitpick-server
# Owned by the unprivileged user, so a fresh volume mounted here is writable.
COPY --from=build --chown=65532:65532 /out/data /data
VOLUME /data
USER 65532:65532
EXPOSE 8484
HEALTHCHECK --interval=15s --timeout=5s --start-period=5s --retries=3 \
  CMD ["/nitpick-server", "-healthcheck", "-addr=:8484"]
ENTRYPOINT ["/nitpick-server"]
CMD ["-addr=:8484", "-data=/data"]
