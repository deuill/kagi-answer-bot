# Kagi Quick Answers Bot

A [XMPP][xmpp] bot for querying [Kagi Quick Answers][kagi-quick-answer]. Intended to be deployed in
smaller instances against a limited number of users, and uses a *personal* login token for requests.

## Building and Installing

Installing `kagi-answer-bot` locally requires that you have Go installed, at a minimum. To install,
simply run the following command:

```sh
$ go install go.deuill.org/webhook-gateway/cmd/webhook-gateway@latest
```

The `kagi-answer-bot` binary should be placed in your `$GOBIN` path. Pre-built binaries are not
provided at this time.

## Configuration

All configuration is made via environment variables, e.g. for a typical setup:

```sh
$ KAGI_ANSWER_BOT_JID=answerbot@example.com
$ KAGI_ANSWER_BOT_PASSWORD=changeme
$ KAGI_ANSWER_BOT_LOGIN_TOKEN=personallogintoken
$ export KAGI_ANSWER_BOT_JID KAGI_ANSWER_BOT_PASSWORD KAGI_ANSWER_BOT_LOGIN_TOKEN
```

The `JID` and `PASSWORD` variables should correspond to the username and password used to
authenticate against an existing, public XMPP server.

The `LOGIN_TOKEN` variable should be derived from the value of the `token` parameter of a [private
browser session link][kagi-session-link].

Production deployments of this bot **should** restrict the JIDs that are allowed to send queries to
a limited number of JIDs -- this is possible via the `KAGI_ANSWER_BOT_ALLOWED_JIDS` environment
variable:

``` sh
$ KAGI_ANSWER_BOT_ALLOWED_JIDS="user@example.com foo@bar.com"
$ export KAGI_ANSWER_BOT_ALLOWED_JIDS
```

You may have to enable StartTLS for some servers which don't support direct TLS connections:

``` sh
$ KAGI_ANSWER_BOT_USE_STARTTLS=true
$ export KAGI_ANSWER_BOT_USE_STARTTLS
```

Local development and servers without any TLS support (seriously, don't use these) may need to
either have TLS certificate verification, or TLS itself, disabled, e.g:

``` sh
$ KAGI_ANSWER_BOT_NO_VERIFY_TLS=true # Accept all certificates, including self-signed.
$ export KAGI_ANSWER_BOT_NO_VERIFY_TLS
```

``` sh
$ KAGI_ANSWER_BOT_NO_TLS=true # Connect to XMPP server over plain-text.
$ export KAGI_ANSWER_BOT_NO_TLS
```

## Deployment

### Bare Metal

Deploying to bare metal requires that you have the `kagi-answer-bot` binary built and installed
somewhere in your local system, preferably in your `$PATH`. Assuming the correct environment
variables are exported, you can simply run the `kagi-answer-bot` binary:

```sh
$ kagi-answer-bot
2025-06-08T12:16:35.622+0100	INFO	Initializing bot	{"name": "kagi-answer-bot"}
2025-06-08T12:16:35.930+0100	INFO	Bot initialized and ready to operate	{"name": "kagi-answer-bot"}
```

The above should suffice for most public XMPP servers that allow for TLS connections to be made; see
the section on configuration for advice on more esoteric setups.

### Containers

Containers are built and pushed to Github's container repository on every commit to `trunk`; to run
the latest version available with Docker (though Podman should also work), and assuming the same
environment variables as above:

```sh
docker run --rm -e KAGI_ANSWER_BOT_JID -e KAGI_ANSWER_BOT_PASSWORD -e KAGI_ANSWER_BOT_LOGIN_TOKEN ghcr.io/deuill/kagi-answer-bot:latest
2025-06-08T12:16:35.622+0100	INFO	Initializing bot	{"name": "kagi-answer-bot"}
2025-06-08T12:16:35.930+0100	INFO	Bot initialized and ready to operate	{"name": "kagi-answer-bot"}
```

No additional setup should be required.

## License

All code in this repository is covered by the terms of the MIT License, the full text of which can be found in the LICENSE file.

[kagi-quick-answer]:https://help.kagi.com/kagi/ai/quick-answer.html
[kagi-session-link]: https://help.kagi.com/kagi/privacy/private-browser-sessions.html
[xmpp]: https://xmpp.org
