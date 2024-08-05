FROM alpine
EXPOSE 443
ENTRYPOINT ["/usr/bin/akash-rpc-proxy"]
COPY *.apk /tmp/
RUN apk add --allow-untrusted /tmp/*.apk
