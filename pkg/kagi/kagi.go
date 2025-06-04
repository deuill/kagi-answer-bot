package kagi

import (
	// Standard library
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	// Third-party packages
	"github.com/go-joe/joe"
)

// Error messages.
var (
	errNoClientBot      = errors.New("no valid bot instance assigned to client")
	errEmptyLoginToken  = errors.New("non-empty login token required for client")
	errAnswerEmptyQuery = errors.New("empty query given for answer")
	errAnswerRequest    = errors.New("failed making HTTP request for answers")
)

// Bot response messages.
const (
	msgAnswerProgress = "Checking answers, please wait..."
	msgAnswerFailed   = "Failed to get answer"
)

const (
	// General-use values.
	sessionCookieName = "kagi_session"          // The cookie name used for cookie-based authentication against Kagi.
	defaultUserAgent  = "kagi-answer-bot/0.1.0" // The HTTP user agent to use when making requests against Kagi.

	// Values for Kagi Answers integration.
	answerBaseURL    = "https://kagi.com/mother/context" // The base URL for Kagi Quick Answer HTTP requests.
	answerQueryParam = "q"                               // The query parameter name used for Kagi Quick Answer queries.
)

// A Client represents a method of making calls to Kagi API endpoints, in support of bot interactions.
type Client struct {
	// Configuration values.
	loginToken string
	userAgent  string

	// Other internal fields.
	bot       Bot
	transport http.RoundTripper
}

// A Bot represents an automated user that provides the ability of pushing (or "saying") plain-text
// messages out to a remote endpoint. It's generally implemented by the [joe.Bot] type.
type Bot interface {
	Say(to, msg string, args ...any)
}

// A ClientOption is any function that can modify the internal state of a given [Client].
type ClientOption = func(*Client) error

// WithBot assigns a [Bot] instance to the given [Client], allowing for outgoing chat interactions.
func WithBot(bot Bot) ClientOption {
	return func(c *Client) error {
		c.bot = bot
		return nil
	}
}

// WithLoginToken enables authenticated requests to Kagi APIs against the given login token, see
// https://help.kagi.com/kagi/privacy/private-browser-sessions.html for more information.
func WithLoginToken(key string) ClientOption {
	return func(c *Client) error {
		c.loginToken = key
		return nil
	}
}

// WithUserAgent overrides the default HTTP User-Agent string passed into outgoing HTTP requests to
// Kagi API endpoints.
func WithUserAgent(agent string) ClientOption {
	return func(c *Client) error {
		c.userAgent = agent
		return nil
	}
}

// NewClient returns a Kagi API client for the given options, or an error if initializing a client
// fails for any reason.
func NewClient(options ...ClientOption) (*Client, error) {
	var c = &Client{
		userAgent: defaultUserAgent,
		transport: http.DefaultTransport,
	}

	for _, fn := range options {
		if err := fn(c); err != nil {
			return nil, err
		}
	}

	switch {
	case c.bot == nil:
		return nil, errNoClientBot
	case c.loginToken == "":
		return nil, errEmptyLoginToken
	}

	return c, nil
}

// HandleEvent processes incoming events from a [joe.Brain] instance this [Client] has presumably
// been registered against. By default, all incoming queries will be handled as Kagi Quick Answer
// queries, and responses, including errors, will be returned verbatim to the attached [Bot] in
// Markdown format.
func (c *Client) HandleEvent(ctx context.Context, e joe.ReceiveMessageEvent) error {
	c.bot.Say(e.Channel, msgAnswerProgress)

	result, err := c.Answer(ctx, e.Text)
	if err != nil {
		result = fmt.Sprintf("%s: %s", msgAnswerFailed, err)
	}

	c.bot.Say(e.Channel, result)
	return nil
}

// AnswerResponse represents structured data returned in response to the Kagi Quick Answer API.
type answerResponse struct {
	Data struct {
		Markdown           string `json:"markdown"`
		MarkdownReferences string `json:"md_references"`
	} `json:"output_data"`
}

// Fetch a quick answer from Kagi, as synthesized from multiple sources. The result will be a Markdown
// encoded string if the query was successful, or an error if not.
func (c *Client) Answer(ctx context.Context, query string) (string, error) {
	if query == "" {
		return "", errAnswerEmptyQuery
	}

	u := fmt.Sprintf("%s?%s=%s", answerBaseURL, answerQueryParam, url.QueryEscape(query))
	req, err := http.NewRequestWithContext(ctx, "GET", u, nil)
	if err != nil {
		return "", fmt.Errorf("%w: %w", errAnswerRequest, err)
	}

	req.Header.Add("Cookie", fmt.Sprintf("%s=%s", sessionCookieName, c.loginToken))
	req.Header.Add("User-Agent", c.userAgent)

	h := &http.Client{Transport: c.transport}
	resp, err := h.Do(req)
	if err != nil {
		return "", fmt.Errorf("%w: %w", errAnswerRequest, err)
	} else if resp.StatusCode >= 400 && resp.StatusCode <= 599 {
		return "", fmt.Errorf("%w: %s received from server", errAnswerRequest, resp.Status)
	}

	defer resp.Body.Close() //nolint:errcheck
	var answer answerResponse

	if err := json.NewDecoder(resp.Body).Decode(&answer); err != nil {
		return "", fmt.Errorf("%w: %w", errAnswerRequest, err)
	}

	result := answer.Data.Markdown
	if answer.Data.MarkdownReferences != "" {
		result += "\n---\nReferences:\n" + answer.Data.MarkdownReferences
	}

	return strings.TrimSpace(result), nil
}
