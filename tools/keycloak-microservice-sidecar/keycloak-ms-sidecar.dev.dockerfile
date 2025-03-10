FROM node:12.18-alpine AS base

WORKDIR /opt/keycloaksidecar

RUN apk --no-cache add \
    bash \
    g++ \
    ca-certificates \
    lz4-dev \
    musl-dev \
    cyrus-sasl-dev \
    openssl-dev \
    make \
    python

RUN apk add --no-cache --virtual .build-deps \
    gcc \
    zlib-dev \
    libc-dev \
    bsd-compat-headers \
    py-setuptools \
    bash

CMD ["tail", "-f", "/dev/null"]


HEALTHCHECK --start-period=2m --interval=30s --timeout=10s --retries=3 \
    CMD curl -f http://localhost:9000/health || exit 1
