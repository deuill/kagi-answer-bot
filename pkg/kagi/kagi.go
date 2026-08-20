package kagi

import (
	// Standard library
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	// Third-party packages
	"github.com/go-joe/joe"
	"go.uber.org/zap"
)

// Error messages.
var (
	errNoClientBot      = errors.New("no valid bot instance assigned to client")
	errEmptyLoginToken  = errors.New("non-empty login token required for client")
	errAnswerEmptyQuery = errors.New("empty query given for answer")
)

// Bot response messages.
const (
	msgAnswerProgress = "Checking answers, please wait..."
	msgAnswerFailed   = "Failed to get answer"
	msgEmptyAnswer    = "Empty answer returned by Kagi, try again in a bit..."
)

const (
	// General-use values.
	sessionCookieName = "kagi_session"          // The cookie name used for cookie-based authentication against Kagi.
	defaultUserAgent  = "kagi-answer-bot/0.2.0" // The HTTP user agent to use when making requests against Kagi.
	maxBufferSize     = 1024 * 1024

	// Values for Kagi Search.
	searchBaseURL    = "https://kagi.com/socket/search?q=%s"
	searchDataPrefix = "data:"
	searchResultsTag = "search_results_json"

	// Values for Kagi Quick Answer.
	answerBaseURL = "https://assistant-api.kagi.com/mother/context"
	answerPrefix  = "final:"

	// The amount of time we'll wait before cancelling an in-flight request.
	defaultRequestTimeout = 10 * time.Second
)

// A Client represents a method of making calls to Kagi API endpoints, in support of bot interactions.
type Client struct {
	// Configuration values.
	loginToken string
	userAgent  string

	// Other internal fields.
	bot    Bot
	client http.Client
	logger *zap.Logger
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

// WithLogger sets the [zap.Logger] to use in emitting logs, overriding the no-op default logger.
func WithLogger(logger *zap.Logger) ClientOption {
	return func(c *Client) error {
		c.logger = logger
		return nil
	}
}

// NewClient returns a Kagi API client for the given options, or an error if initializing a client
// fails for any reason.
func NewClient(options ...ClientOption) (*Client, error) {
	var c = &Client{
		userAgent: defaultUserAgent,
		client: http.Client{
			Transport: http.DefaultTransport,
			Timeout:   defaultRequestTimeout,
		},
		logger: zap.NewNop(),
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
	c.logger.Debug("Handling incoming event", zap.Any("event", e))

	result, err := c.Answer(ctx, e.Text)
	if err != nil {
		result = fmt.Sprintf("%s: %s", msgAnswerFailed, err)
	} else if result == "" {
		result = msgEmptyAnswer
	}

	c.bot.Say(e.Channel, result)
	return nil
}

// Fetch a quick answer from Kagi, as synthesized from multiple sources. The result will be a Markdown
// encoded string if the query was successful, or an error if not.
func (c *Client) Answer(ctx context.Context, query string) (string, error) {
	if query == "" {
		return "", errAnswerEmptyQuery
	}

	// Fetch search results for query.
	body, err := c.sendRequest(ctx, http.MethodGet, fmt.Sprintf(searchBaseURL, url.QueryEscape(query)), nil)
	if err != nil {
		return "", fmt.Errorf("sending search request: %w", err)
	}

	result, err := c.parseSearchResult(body)
	if err != nil {
		return "", fmt.Errorf("parsing search response: %w", err)
	}

	// Synthesize quick answer from search results and original query.
	req := answerRequest{Query: query, searchResult: *result}
	body, err = c.sendRequest(ctx, http.MethodPost, answerBaseURL, req)
	if err != nil {
		return "", fmt.Errorf("sending answer request: %w", err)
	}

	answer, err := c.parseAnswerResult(body)
	if err != nil {
		return "", fmt.Errorf("parsing answer response: %w", err)
	}

	return answer, nil
}

// SendRequest processes and sends an HTTP request for the given HTTP method and URL, with optional,
// JSON-encoded body data. Successful calls will have the response body returned as an [io.ReadCloser],
// with callers being responsible for reading and closing the response correctly.
func (c *Client) sendRequest(ctx context.Context, method, url string, data any) (io.ReadCloser, error) {
	var body bytes.Buffer
	if data != nil {
		if err := json.NewEncoder(&body).Encode(data); err != nil {
			return nil, fmt.Errorf("encoding request body to JSON: %w", err)
		}
	}

	req, err := http.NewRequestWithContext(ctx, method, url, &body)
	if err != nil {
		return nil, fmt.Errorf("initializing request: %w", err)
	}

	c.logger.Debug("sending HTTP request", zap.String("method", method), zap.String("url", url))

	req.Header.Add("Cookie", fmt.Sprintf("%s=%s", sessionCookieName, c.loginToken))
	req.Header.Add("User-Agent", c.userAgent)

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("sending request: %w", err)
	} else if resp.StatusCode >= 400 && resp.StatusCode <= 599 {
		return nil, fmt.Errorf("unexpected %s status received", resp.Status)
	}

	return resp.Body, nil
}

// SearchResponse represents data returned by the Kagi Search socket API.
type searchResponse struct {
	Tag     string `json:"tag"`
	Payload string `json:"payload"`
	Version string `json:"kagi_version"`
}

// SearchResult represents the payload for search results returned in [SearchResponse] values.
type searchResult struct {
	Items []struct {
		Title   string `json:"title"`
		URL     string `json:"url"`
		Snippet string `json:"snippet"`
		Rank    int64  `json:"rank"`
		Source  string `json:"source"`
	} `json:"items"`
	AnswerBox any `json:"answer_box"`
}

// ParseSearchResult processes the given response body, intended to correspond to a search query on
// the Kagi socket API, and returns a concrete [searchResult] on successful calls. The [io.ReadCloser]
// is closed after calls to this function, and cannot be reused.
func (c *Client) parseSearchResult(body io.ReadCloser) (*searchResult, error) {
	var scanner = bufio.NewScanner(body)
	scanner.Buffer([]byte{}, maxBufferSize)

	defer body.Close() //nolint:errcheck

	for scanner.Scan() {
		data, ok := bytes.CutPrefix(scanner.Bytes(), []byte(searchDataPrefix))
		if ok {
			var resp []searchResponse
			data = bytes.Trim(data, "\x00")
			if err := json.Unmarshal(data, &resp); err != nil {
				continue
			}
			var result searchResult
			for _, r := range resp {
				if r.Tag == searchResultsTag {
					if err := json.Unmarshal([]byte(r.Payload), &result); err != nil {
						return nil, fmt.Errorf("parsing search result payload: %w", err)
					}
					return &result, nil
				}
			}
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, err
	}

	return nil, fmt.Errorf("no search results found in response")
}

// AnswerRequest represents the body for HTTP requests sent to the Kagi Answer API.
type answerRequest struct {
	Query string `json:"query"`
	searchResult
}

// AnswerResponse represents the response body for HTTP requests sent to the Kagi Answer API.
type answerResponse struct {
	Data struct {
		Markdown           string `json:"markdown"`
		MarkdownReferences string `json:"md_references"`
	} `json:"output_data"`
}

// ParseAnswerResult processes the given response body, intended to correspond to a answer query on
// the Kagi Answer API, and returns a concrete [searchResult] on successful calls. The [io.ReadCloser]
// is closed after calls to this function, and cannot be reused.
func (c *Client) parseAnswerResult(body io.ReadCloser) (string, error) {
	var scanner = bufio.NewScanner(body)
	scanner.Buffer([]byte{}, maxBufferSize)

	defer body.Close() //nolint:errcheck

	for scanner.Scan() {
		data, ok := bytes.CutPrefix(scanner.Bytes(), []byte(answerPrefix))
		if ok {
			var resp answerResponse
			data = bytes.Trim(data, "\x00")
			if err := json.Unmarshal(data, &resp); err != nil {
				return "", fmt.Errorf("parsing answer payload: %w", err)
			}
			answer := resp.Data.Markdown
			if resp.Data.MarkdownReferences != "" {
				answer += "\n---\nReferences:\n" + resp.Data.MarkdownReferences
			}
			return answer, nil
		}
	}

	if err := scanner.Err(); err != nil {
		return "", err
	}

	return "", fmt.Errorf("no answer found in response")
}
