FROM scratch
USER 8737
COPY dist/operator-linux-amd64 /bin/
ENTRYPOINT [ "operator" ]
