# syntax=docker/dockerfile:1.7
FROM golang:1.27.3-alpine3.24 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY . .
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o /voicebot ./cmd/voicebot

FROM alpine:3.24
RUN apk add --no-cache ca-certificates ffmpeg && addgroup -S app && adduser -S -G app app
COPY --from=build /voicebot /usr/local/bin/voicebot
USER app
ENTRYPOINT ["/usr/local/bin/voicebot"]
