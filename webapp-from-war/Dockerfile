# Guacamole web app for versions that have no arm64 image on Docker Hub:
# Tomcat 9 plus the release WAR from archive.apache.org (checked by SHA-256).
# Reads /etc/guacamole (user-mapping.xml is mounted by compose).
FROM tomcat:9.0-jre17-temurin

ARG GUAC_VERSION
ENV GUACAMOLE_HOME=/etc/guacamole

RUN set -eu; \
    base="https://archive.apache.org/dist/guacamole/${GUAC_VERSION}/binary"; \
    cd /tmp; \
    curl -fsSLO "$base/guacamole-${GUAC_VERSION}.war"; \
    curl -fsSLO "$base/guacamole-${GUAC_VERSION}.war.sha256"; \
    sha256sum -c "guacamole-${GUAC_VERSION}.war.sha256"; \
    mv "guacamole-${GUAC_VERSION}.war" /usr/local/tomcat/webapps/guacamole.war; \
    rm -f /tmp/*.sha256; \
    mkdir -p /etc/guacamole; \
    printf 'guacd-hostname: guacd\nguacd-port: 4822\n' > /etc/guacamole/guacamole.properties

EXPOSE 8080
CMD ["catalina.sh", "run"]
