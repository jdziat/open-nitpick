# syntax=docker/dockerfile:1
#
# nitpick-server, which hosts the notary and the hub UI. Build from the
# repository root:
#   docker build -f deploy/docker/Dockerfile -t nitpick-server .

# The web app is built first, from the lockfile with install scripts off. It also
# reads the server's templates (the sign-in page uses the same Tailwind classes), so
# Tailwind can see which of them are used.
FROM node:24-alpine AS ui
WORKDIR /src/ui
COPY ui/package.json ui/package-lock.json ./
RUN npm ci --ignore-scripts --no-audit --no-fund
COPY ui/ ./
COPY internal/server/templates /src/internal/server/templates
RUN npm run build

FROM golang:1.27-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
# The built web app is copied into the directory the server embeds, and embedded by
# the compile after it. A go build run without these steps produces a server whose
# UI pages say the UI was not built.
COPY --from=ui /src/ui/dist ui/dist
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
