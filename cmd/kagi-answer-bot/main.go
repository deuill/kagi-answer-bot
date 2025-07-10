package main

import (
	// Standard library
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	// Internal packages
	"go.deuill.org/kagi-answer-bot/pkg/kagi"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"

	// Third-party packages
	"github.com/go-joe/joe"
	"go.deuill.org/joe-xmpp-adapter"
)

func main() {
	var logLevel = zap.InfoLevel
	var err error

	if l := os.Getenv("KAGI_ANSWER_BOT_LOG_LEVEL"); l != "" {
		if logLevel, err = zapcore.ParseLevel(l); err != nil {
			fmt.Fprintf(os.Stderr, "Failed to set log level '%s': %s", l, err)
		}
	}

	ctx, _ := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGQUIT, syscall.SIGTERM)
	bot := joe.New(
		"kagi-answer-bot",
		joe.WithContext(ctx),
		joe.WithLogLevel(logLevel),
		xmpp.Adapter(ctx, xmpp.Config{
			JID:         os.Getenv("KAGI_ANSWER_BOT_JID"),
			Password:    os.Getenv("KAGI_ANSWER_BOT_PASSWORD"),
			NoTLS:       os.Getenv("KAGI_ANSWER_BOT_NO_TLS") == "true",
			NoVerifyTLS: os.Getenv("KAGI_ANSWER_BOT_NO_VERIFY_TLS") == "true",
			UseStartTLS: os.Getenv("KAGI_ANSWER_BOT_USE_STARTTLS") == "true",
			AllowedJIDs: os.Getenv("KAGI_ANSWER_BOT_ALLOWED_JIDS"),
		}),
	)

	client, err := kagi.NewClient(
		kagi.WithLoginToken(os.Getenv("KAGI_ANSWER_BOT_LOGIN_TOKEN")),
		kagi.WithLogger(bot.Logger),
		kagi.WithBot(bot),
	)

	if err != nil {
		bot.Logger.Fatal(err.Error())
	}

	bot.Brain.RegisterHandler(client.HandleEvent)
	if err := bot.Run(); err != nil {
		bot.Logger.Fatal(err.Error())
	}
}
