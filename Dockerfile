# Privileged controller image, tagged whalias:local by setup.
# Run on the ordinary bridge network whalias, not --internal and not
# 10.254.253.0/24. Publish 127.0.0.1 to container 51820/udp and
# 127.0.0.1:5387 TCP and UDP. Do not publish 8443. Mount the Docker socket and
# bind-mount the host public-key directory at /keys. The private key stays in
# the data volume, which must not be under /keys.
#
#   docker run -d --name whalias --network whalias --privileged \
#     -p 127.0.0.1::51820/udp -p 127.0.0.1:5387:5387/tcp -p 127.0.0.1:5387:5387/udp \
#     -v /var/run/docker.sock:/var/run/docker.sock -v <host-public-key-dir>:/keys -v whalias-data:/data \
#     whalias:local
FROM golang:1.26.0-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
COPY cmd ./cmd
COPY internal ./internal
ENV CGO_ENABLED=0
RUN go build -o /out/whalias ./cmd/whalias

FROM alpine:3.22
RUN apk add --no-cache iproute2 iptables wireguard-tools
COPY --from=build /out/whalias /usr/local/bin/whalias
EXPOSE 51820/udp 5387/tcp 5387/udp
ENTRYPOINT ["/usr/local/bin/whalias"]
