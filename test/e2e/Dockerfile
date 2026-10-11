# An Alpine sshd for pfwd's end-to-end tests. WITH_SS=1 adds iproute2's ss;
# without it, pfwd falls back to /proc/net/tcp, as on busybox systems.
FROM alpine:3.22
ARG WITH_SS=0
RUN apk add --no-cache openssh-server python3 \
 && if [ "$WITH_SS" = 1 ]; then apk add --no-cache iproute2-ss; fi \
 && adduser -D -s /bin/sh pfwd \
 && echo "pfwd:$(head -c 24 /dev/urandom | base64)" | chpasswd
COPY sshd_config /etc/ssh/sshd_config
COPY entrypoint.sh /entrypoint.sh
EXPOSE 22
ENTRYPOINT ["/bin/sh", "/entrypoint.sh"]
