# Runs MikroTik Cloud Hosted Router (CHR) under QEMU so the acceptance suite can
# be executed against a specific RouterOS version.
#
# CI currently depends on prebuilt third-party images that exist only for a few
# RouterOS versions. This builds an equivalent from MikroTik's official CHR
# download for ANY version, so a schema change can be verified against the exact
# release that introduced it.
#
#   docker build --build-arg ROS_VERSION=7.23 -t chr:7.23 test/chr
#   docker run --rm -d --name chr --device /dev/kvm \
#     -p 8728:8728 -p 8729:8729 -p 443:443 chr:7.23
#
# /dev/kvm is used when present and falls back to emulation when it is not, so
# the same image works on a CI runner with nested virtualisation and on a
# developer laptop without it.

FROM debian:12-slim

ARG ROS_VERSION
ARG CHR_SHA256=""

RUN apt-get update \
    && apt-get install --no-install-recommends -y \
        qemu-system-x86 \
        qemu-utils \
        ca-certificates \
        curl \
        unzip \
        procps \
        iproute2 \
    && apt-get clean \
    && find /var/lib/apt/lists -mindepth 1 -delete

WORKDIR /routeros

# Fetch and unpack the official CHR raw disk image. MikroTik's CDN resets long
# transfers, so retry until the archive passes an integrity check.
RUN set -eux; \
    test -n "${ROS_VERSION}"; \
    url="https://download.mikrotik.com/routeros/${ROS_VERSION}/chr-${ROS_VERSION}.img.zip"; \
    for attempt in 1 2 3 4 5; do \
        curl -fsSL --retry 5 --retry-delay 3 --retry-all-errors -C - -o chr.img.zip "$url" || true; \
        if unzip -t -q chr.img.zip; then break; fi; \
        echo "attempt ${attempt} incomplete, resuming"; \
    done; \
    unzip -t -q chr.img.zip; \
    if [ -n "${CHR_SHA256}" ]; then echo "${CHR_SHA256}  chr.img.zip" | sha256sum -c -; fi; \
    unzip -o -q chr.img.zip; \
    mv "chr-${ROS_VERSION}.img" chr.img; \
    rm chr.img.zip

COPY entrypoint.sh /routeros/entrypoint.sh
RUN chmod +x /routeros/entrypoint.sh

# api, api-ssl, REST over TLS
EXPOSE 8728 8729 443

ENV CHR_MEMORY=512 \
    CHR_SMP=2 \
    CHR_INTERFACES=4 \
    CHR_BOOT_TIMEOUT=600

ENTRYPOINT ["/routeros/entrypoint.sh"]
