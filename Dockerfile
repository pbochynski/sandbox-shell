FROM golang:1.26-alpine AS builder
WORKDIR /src
COPY go.mod ./
COPY *.go ./
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o /sandbox-shell .

FROM alpine:3.20
RUN apk add --no-cache \
    ttyd \
    bash \
    curl \
    wget \
    jq \
    vim \
    git \
    python3 \
    make \
    netcat-openbsd \
    iproute2 \
    util-linux \
    xxd \
    coreutils \
    findutils \
    grep \
    sed \
    gawk \
    ca-certificates

RUN addgroup -g 101 sandbox && \
    adduser -D -u 65532 -G sandbox -h /home/sandbox sandbox

COPY --from=builder /sandbox-shell /sandbox-shell

USER 65532
WORKDIR /home/sandbox
EXPOSE 7681 7682
ENTRYPOINT ["/sandbox-shell"]
