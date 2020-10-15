FROM golang AS builder

COPY . /go/src/github.com/clive2000/rclone/
WORKDIR /go/src/github.com/clive2000/rclone/

RUN \
  CGO_ENABLED=0 \
  make
RUN ./rclone version

# Begin final image
FROM alpine:latest

RUN apk --no-cache add ca-certificates fuse tzdata && \
  echo "user_allow_other" >> /etc/fuse.conf

COPY --from=builder /go/src/github.com/clive2000/rclone/rclone /usr/local/bin/

ENTRYPOINT [ "rclone" ]

WORKDIR /data
ENV XDG_CONFIG_HOME=/config
