FROM golang:1.27.3-alpine3.24 AS build
WORKDIR /src
COPY . .
RUN go mod tidy && CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o /voicebot ./cmd/voicebot

FROM alpine:3.24
RUN apk add --no-cache ca-certificates && addgroup -S app && adduser -S -G app app
COPY --from=build /voicebot /usr/local/bin/voicebot
USER app
ENTRYPOINT ["/usr/local/bin/voicebot"]
