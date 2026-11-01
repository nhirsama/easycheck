FROM golang:1.24-alpine AS build

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
COPY qdgj ./qdgj

ARG TARGETOS
ARG TARGETARCH
RUN CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH:-amd64} \
    go build -trimpath -ldflags="-s -w" -o /out/easycheck ./cmd/easycheck

FROM alpine:3.22

RUN apk add --no-cache ca-certificates tzdata \
    && addgroup -S easycheck \
    && adduser -S -G easycheck -h /data easycheck \
    && mkdir -p /data \
    && chown easycheck:easycheck /data

COPY --from=build --chown=easycheck:easycheck /out/easycheck /usr/local/bin/easycheck

ENV EASYCHECK_SESSION_FILE=/data/session.json
WORKDIR /data
USER easycheck

ENTRYPOINT ["/usr/local/bin/easycheck"]
