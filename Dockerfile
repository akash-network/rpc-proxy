FROM alpine:3.24
EXPOSE 443
ENTRYPOINT ["/usr/bin/akash-rpc-proxy"]
COPY *.apk /tmp/
# TODO: once an Alpine ca-certificates-bundle drops emSign ECC Root CA - G3,
# fail the build if its SHA-256 fingerprint (86A1ECBA...BE6B) is still in
# /etc/ssl/certs/ca-certificates.crt. The bundle has no labels, so grepping
# for the name always finds nothing.
RUN apk upgrade --no-cache ca-certificates-bundle \
 && apk add --no-cache --allow-untrusted /tmp/*.apk \
 && rm /tmp/*.apk
