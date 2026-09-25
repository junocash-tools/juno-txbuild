FROM --platform=linux/amd64 ubuntu:24.04@sha256:1e0a86e57d247923571b75e0aaf48a1449cf8c543d51fb3e07a4a7d7bfa79316

# Released scanner used for compatibility tests. v1.4.7-mainnet is the
# production mainnet scanner and does not send event_epoch in /v1/health.
ARG JUNO_SCAN_VERSION=v1.4.7-mainnet
ARG JUNO_SCAN_LINUX_AMD64_SHA256=e4a70b318c021920f11f6a8f01c67a1e0791e8b35a92df509d8024438d2c2969

RUN apt-get update && apt-get install -y --no-install-recommends \
    ca-certificates \
    curl \
    tar \
  && rm -rf /var/lib/apt/lists/*

WORKDIR /tmp
RUN curl -fsSLo "juno-scan.tar.gz" "https://github.com/junocash-tools/juno-scan/releases/download/${JUNO_SCAN_VERSION}/juno-scan_${JUNO_SCAN_VERSION}_linux_amd64.tar.gz" \
 && echo "${JUNO_SCAN_LINUX_AMD64_SHA256}  juno-scan.tar.gz" | sha256sum -c - \
 && tar -xzf juno-scan.tar.gz \
 && install -m 0755 juno-scan /usr/local/bin/juno-scan \
 && rm -rf /tmp/*

VOLUME ["/data"]

ENTRYPOINT ["juno-scan"]
