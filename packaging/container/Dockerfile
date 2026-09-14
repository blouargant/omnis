# Reference image: omnis-server as a supervisord service inside a per-user
# container (docs/multi-user-containers.md). The platform owns the base image
# (Unix accounts, NFS home mount, supervisord, the home-init service and the
# envsubst entrypoint); this layer only adds omnis.
#
# Build from the repo root after `make package`:
#   docker build -f packaging/container/Dockerfile \
#     --build-arg BASE_IMAGE=<platform base image> -t omnis-user:dev .
ARG BASE_IMAGE=debian:bookworm-slim
FROM ${BASE_IMAGE}

# The .deb lays binaries at /usr/bin, the system config at /etc/omnis (the
# default system layer — no OMNIS_SYSTEM_CONFIG_DIR needed) and the web UI at
# /usr/share/omnis/web. It declares python3 as a dependency (shipped k8s hook).
COPY dist/omnis_*_linux_x86_64.deb /tmp/omnis.deb
RUN apt-get update \
 && apt-get install -y --no-install-recommends /tmp/omnis.deb supervisor gettext-base \
 && rm -rf /var/lib/apt/lists/* /tmp/omnis.deb

# Image-side server settings (no browser, no self-update, no A2A, identity header).
COPY packaging/container/server.yaml /etc/omnis/server.yaml

# supervisord program TEMPLATE — the entrypoint renders it per user:
#   envsubst < /etc/omnis/supervisord/omnis-server.conf.tmpl \
#     > /etc/supervisor/conf.d/omnis-server.conf
COPY packaging/supervisord/omnis-server.conf /etc/omnis/supervisord/omnis-server.conf.tmpl

# No ENTRYPOINT/CMD: the platform's entrypoint starts supervisord.
