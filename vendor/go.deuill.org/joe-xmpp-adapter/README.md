# Joe Bot - XMPP Adapter

This repository contains a module for the [Joe Bot library][joe-bot] for connecting to [XMPP][xmpp] servers.

## Getting Started

This library is packaged as [Go module][go-modules]. You can get it via:

```
go get go.deuill.org/joe-xmpp-adapter
```

### Example Usage

In order to connect your bot to slack you can simply pass it as module when
creating a new bot:

```go
package main

import (
	"os"

	"github.com/go-joe/joe"
	"go.deuill.org/joe-xmpp-adapter"
)

func main() {
	ctx, _ := signal.NotifyContext(context.Background(), os.Interrupt, os.Kill)
	bot := joe.New(
		"example-bot",
		xmpp.Adapter(ctx, xmpp.Config{
			JID:         os.Getenv("XMPP_JID"),
			Password:    os.Getenv("XMPP_PASSWORD"),
			NoTLS:       os.Getenv("XMPP_NO_TLS") == "true",
			UseStartTLS: os.Getenv("XMPP_USE_STARTTLS") == "true",
		}),
	)

	bot.Respond("ping", Pong)

	err := bot.Run()
	if err != nil {
		bot.Logger.Fatal(err.Error())
	}

	<-ctx.Done()
}
```

The adapter will emit the following events to the robot brain:

- `joe.ReceiveMessageEvent`

## License

All code in this repository is covered by the terms of the MIT License, the full text of which can
be found in the [LICENSE](LICENSE) file.

[joe-bot]: https://github.com/go-joe/joe
[xmpp]: https://xmpp.org
