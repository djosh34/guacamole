# Minimal SSH target for the Guacamole test stack.
FROM alpine:3.22

RUN apk add --no-cache openssh bash ncurses less coreutils procps \
 && ssh-keygen -A \
 && adduser -D -s /bin/bash tester \
 && echo 'tester:tester' | chpasswd \
 && sed -i -e 's/^#\?PasswordAuthentication.*/PasswordAuthentication yes/' \
           -e 's/^#\?KbdInteractiveAuthentication.*/KbdInteractiveAuthentication yes/' /etc/ssh/sshd_config \
 && printf 'export PS1="\\u@\\h:\\w\\$ "\n' > /home/tester/.bashrc \
 && printf '[ -f ~/.bashrc ] && . ~/.bashrc\n' > /home/tester/.bash_profile \
 && chown tester:tester /home/tester/.bashrc /home/tester/.bash_profile

EXPOSE 22
CMD ["/usr/sbin/sshd", "-D", "-e"]
