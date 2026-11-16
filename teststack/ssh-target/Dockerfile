# SSH target for the Guacamole test stack. It has what the real use needs:
# a shell, grep, tail, a log stream that never ends (logstream) and long log
# files in /var/log/app. The old libssh2 in guacd 1.4 needs ssh-rsa host keys.
FROM alpine:3.22

RUN apk add --no-cache openssh bash coreutils grep procps \
 && ssh-keygen -A \
 && adduser -D -s /bin/bash tester \
 && echo 'tester:tester' | chpasswd \
 && sed -i -e 's/^#\?PasswordAuthentication.*/PasswordAuthentication yes/' \
           -e 's/^#\?KbdInteractiveAuthentication.*/KbdInteractiveAuthentication yes/' /etc/ssh/sshd_config \
 && printf 'HostKeyAlgorithms +ssh-rsa\nKexAlgorithms +diffie-hellman-group14-sha1\n' >> /etc/ssh/sshd_config \
 && printf 'export PS1="\\u@\\h:\\w\\$ "\n' > /home/tester/.bashrc \
 && printf '[ -f ~/.bashrc ] && . ~/.bashrc\n' > /home/tester/.bash_profile \
 && chown tester:tester /home/tester/.bashrc /home/tester/.bash_profile

COPY logstream /usr/local/bin/logstream
RUN chmod 755 /usr/local/bin/logstream \
 && mkdir -p /var/log/app \
 && awk 'BEGIN { for (i = 1; i <= 20000; i++) { err = i % 50 == 0; \
      printf "2026-01-01T%02d:%02d:%02dZ %s app[%d]: request id=%05d path=/api/v1/items/%d status=%d\n", \
        int(i / 3600), int(i / 60) % 60, i % 60, err ? "ERROR" : "INFO", i % 7, i, i % 313, err ? 500 : 200 } }' \
    > /var/log/app/app.log \
 && chmod 644 /var/log/app/app.log

EXPOSE 22
CMD ["/usr/sbin/sshd", "-D", "-e"]
