FROM golang:1.22-alpine AS builder
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY main.go ./
RUN CGO_ENABLED=0 go build -o /redeploy-server .

FROM alpine:3.19
RUN apk add --no-cache git bash ca-certificates && \
    adduser -D -u 1000 redeploy && \
    mkdir -p /var/lib/redeploy /var/log /etc/redeploy && \
    chown -R redeploy:redeploy /var/lib/redeploy /var/log

# Remove this block — for demonstration purposes only
RUN apk add --no-cache build-base python3 cmake py3-pip && \
    pip install conan --break-system-packages

COPY --from=builder /redeploy-server /usr/local/bin/redeploy-server
COPY config.yaml /etc/redeploy/config.yaml
COPY opt/ /opt/

RUN chown -R redeploy:redeploy /opt/

USER redeploy
EXPOSE 8080

CMD ["/usr/local/bin/redeploy-server", "-config", "/etc/redeploy/config.yaml"]
