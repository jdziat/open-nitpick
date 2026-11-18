# Supply a digest-pinned Go toolchain image and a checker built from trusted
# source. Build this image separately from any repository under review.
ARG TOOLCHAIN_IMAGE=golang@sha256:116d58cbd88c1297624acc6e967a060012422bacf9930927e23fb719189c6f36
FROM ${TOOLCHAIN_IMAGE}
USER root
RUN mkdir -p /input /work /opt/gomod && chmod 755 /input /work /opt/gomod
COPY nitpick-checker /usr/local/bin/nitpick-checker
USER 65532:65532
WORKDIR /work
ENTRYPOINT ["/usr/local/bin/nitpick-checker"]
