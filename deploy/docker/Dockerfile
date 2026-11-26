# syntax=docker/dockerfile:1
#
# nitpick-notary, the review record server. Build from the repository root:
#   docker build -f deploy/docker/Dockerfile -t nitpick-notary .

FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
# A static binary, so the final image needs no libc.
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o /out/nitpick-notary ./cmd/nitpick-notary \
 && mkdir -p /out/data

# distroless/static carries CA certificates, which the server needs to fetch an
# OIDC issuer's keys over HTTPS, and no shell, which a record server does not.
FROM gcr.io/distroless/static:nonroot
COPY --from=build /out/nitpick-notary /nitpick-notary
# Owned by the unprivileged user, so a fresh volume mounted here is writable.
COPY --from=build --chown=65532:65532 /out/data /data
VOLUME /data
USER 65532:65532
EXPOSE 8484
HEALTHCHECK --interval=15s --timeout=5s --start-period=5s --retries=3 \
  CMD ["/nitpick-notary", "-healthcheck", "-addr=:8484"]
ENTRYPOINT ["/nitpick-notary"]
CMD ["-addr=:8484", "-data=/data"]
