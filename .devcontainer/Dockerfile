FROM docker.io/library/golang:1.23

RUN apt-get update && apt-get install -y --no-install-recommends \
    ripgrep \
    make \
    && rm -rf /var/lib/apt/lists/*
