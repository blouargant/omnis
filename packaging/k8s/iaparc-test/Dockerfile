# Shared omnis-server for the IA Parc test platform (cookie identity mode).
# Built by packaging/k8s/iaparc-test/build.sh — do not build by hand.
FROM debian:bookworm-slim
RUN apt-get update \
 && apt-get install -y --no-install-recommends python3 ca-certificates git \
 && rm -rf /var/lib/apt/lists/* \
 && useradd -m -u 10001 -s /bin/bash omnis
COPY bin/omnis bin/omnis-server /usr/bin/
COPY .build/iapcli /usr/bin/iapcli
COPY config/*.json /etc/omnis/
COPY config/hooks/k8s-validate.py /etc/omnis/hooks/k8s-validate.py
COPY registry /etc/omnis/registry
COPY web /usr/share/omnis/web
COPY .build/agentskills /etc/agentskills
# Deployment-only project layer: the IA Parc squad + agent + read-only iapcli
# permissions. The server's cwd is /home/omnis, so this is its `.agents/`
# layer, deep-merged over /etc/omnis (never replacing it).
COPY packaging/k8s/iaparc-test/agents /home/omnis/.agents
RUN chmod 0755 /etc/omnis/hooks/k8s-validate.py
ENV OMNIS_WEB_DIR=/usr/share/omnis/web HOME=/home/omnis OMNIS_HOME=/data
USER 10001
WORKDIR /home/omnis
EXPOSE 8080
ENTRYPOINT ["/usr/bin/omnis-server"]
