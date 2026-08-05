FROM golang:1.25 AS builder

COPY . /go/src/app
WORKDIR /go/src/app

ENV GO111MODULE=on

RUN CGO_ENABLED=0 GOOS=linux go build -o app main.go

FROM alpine:3.24

LABEL org.opencontainers.image.source=https://github.com/SENERGY-Platform/analytics-operator-repo-v2

RUN adduser -D -u 10001 app
WORKDIR /app
COPY --from=builder --chown=app:app /go/src/app/app .
COPY --from=builder --chown=app:app /go/src/app/docs docs
USER app

# Port must match the server default; the previous value probed port 80, where
# nothing listens, so the check could never succeed.
HEALTHCHECK --interval=10s --timeout=5s --retries=3 CMD wget -nv -t1 --spider 'http://localhost:8000/health-check' || exit 1

ENTRYPOINT ["./app"]