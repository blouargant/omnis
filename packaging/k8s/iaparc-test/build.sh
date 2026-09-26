#!/usr/bin/env bash
# Build and push iaparc/omnis-server:dev-<version> for the IA Parc test cluster.
# Requires: docker login to Docker Hub (org "iaparc"), iapcli installed locally,
# the iaparc skills at /etc/agentskills/skills/iaparc.
set -euo pipefail
cd "$(git rev-parse --show-toplevel)"
VERSION=$(git describe --tags --always --dirty)
IMAGE=${IMAGE:-iaparc/omnis-server:dev-${VERSION}}
CGO_ENABLED=0 make build
rm -rf .build && mkdir -p .build/agentskills/skills
cp "$(command -v iapcli)" .build/iapcli
cp -r /etc/agentskills/skills/iaparc .build/agentskills/skills/
docker build -f packaging/k8s/iaparc-test/Dockerfile -t "$IMAGE" .
if [[ "${PUSH:-1}" == "1" ]]; then docker push "$IMAGE"; fi
echo "$IMAGE"
