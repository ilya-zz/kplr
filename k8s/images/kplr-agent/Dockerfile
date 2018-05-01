FROM debian:stable

RUN apt-get -q -y update && \
    apt-get install --no-install-recommends -y -q \
    procps \
    curl \
    less \
    dnsutils

RUN mkdir -p /opt/kplr/agent

COPY ["build/kplr-agent", "config.json", "/opt/kplr/agent/"]

ENTRYPOINT ["/opt/kplr/agent/kplr-agent", "--config-file", "/opt/kplr/agent/config.json"]
