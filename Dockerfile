FROM alpine:3 as certs
RUN apk --update add ca-certificates

FROM scratch
USER 8737
COPY --from=certs /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --chown=8737 empty manifests.yaml
COPY dist/operator-linux-amd64 /bin/operator
ENTRYPOINT [ "/bin/operator" ]
