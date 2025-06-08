FROM docker.io/golang:1.24 AS builder

COPY . /src/
WORKDIR /src/cmd/kagi-answer-bot/

RUN go build -o /kagi-answer-bot

FROM docker.io/debian:stable-slim

RUN apt-get update -y && apt-get install -y --no-install-recommends \
    ca-certificates

COPY --from=builder /kagi-answer-bot /

USER nobody
ENTRYPOINT ["/kagi-answer-bot"]
