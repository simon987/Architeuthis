FROM golang:1.13-alpine as builder

ENV GO111MODULE=on

# Create the user and group files to run unprivileged
RUN mkdir /user && \
    echo 'nobody:x:65534:65534:nobody:/:' > /user/passwd && \
    echo 'nobody:x:65534:' > /user/group

RUN apk update && apk add --no-cache git ca-certificates tzdata
RUN mkdir /build
COPY . /build/
WORKDIR /build

COPY ./ ./
RUN CGO_ENABLED=0 GOOS=linux go build -a -installsuffix cgo -ldflags '-extldflags "-static"' -o architeuthis .

FROM scratch AS final
LABEL author="simon987 <me@simon987.net>"

COPY --from=builder /usr/share/zoneinfo /usr/share/zoneinfo
COPY --from=builder /user/group /user/passwd /etc/
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=builder /build/architeuthis /
COPY --from=builder /build/config.json /

WORKDIR /
USER nobody:nobody
ENTRYPOINT ["/architeuthis"]

EXPOSE 5050
