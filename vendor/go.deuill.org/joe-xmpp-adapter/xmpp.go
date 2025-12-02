package xmpp

import (
	// Standard library
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"time"

	// Third-party packages
	"github.com/go-joe/joe"
	"go.uber.org/zap"
	"mellium.im/sasl"
	"mellium.im/xmlstream"
	"mellium.im/xmpp"
	"mellium.im/xmpp/dial"
	"mellium.im/xmpp/jid"
	"mellium.im/xmpp/stanza"
)

// DefaultAuthMechanisms represents the list of SASL authentication mechanisms this client is allowed
// to use in server authentication.
var defaultAuthMechanisms = []sasl.Mechanism{
	sasl.ScramSha256Plus,
	sasl.ScramSha256,
	sasl.ScramSha1Plus,
	sasl.ScramSha1,
	sasl.Plain,
}

// Config represents required and optional configuration values used in setting up the XMPP bot client.
type Config struct {
	// Required configuration.
	JID string // The JID to use for this client in connections against the XMPP server.

	// Optional configuration.
	Password    string // The password to use in authenticating against the XMPP server.
	NoTLS       bool   // Whether to disable TLS connection to the XMPP server.
	NoVerifyTLS bool   // Whether or not certificates will be verified for TLS connections.
	UseStartTLS bool   // Whether or not connection will be allowed to be made over StartTLS.
	AllowedJIDs string // A space-separated list of JIDs that are allowed to send messages to this adapter.

	// Other fields.
	Logger *zap.Logger // The instance to use for emitting log messages.
}

// A Dialer is any type that can open network connections to an XMPP server for a given JID, and is
// generally fulfilled by the [dial.Dialer] type, except in tests.
type dialer interface {
	Dial(context.Context, string, jid.JID) (net.Conn, error)
}

// A Session is any type that represents an ongoing XMPP client session, and is generally fulfilled
// by the [xmpp.Session] type, except in tests.
type session interface {
	Send(context.Context, xml.TokenReader) error
	Serve(xmpp.Handler) error
	Close() error
}

// A Emitter is any type that allows us to emit messages to upstream adapters, for example, types
// that introspect and manipulate messages before sending back to XMPP.
type emitter interface {
	Emit(any, ...func(joe.Event))
}

// Client represents an active XMPP session against a server, and configuration for handling messages
// against a Joe instance.
type Client struct {
	// XMPP client state.
	jid         jid.JID              // The full JID for this client.
	features    []xmpp.StreamFeature // The XMPP stream features to advertise.
	allowedJIDs map[string]struct{}  // The list of JIDs that we should handle messages from.

	// XMPP and bot connection state.
	dialer  dialer  // The underlying mechanism for making connections to XMPP servers.
	session session // The active XMPP session.
	emitter emitter // The handler for emitted messages emitted to upstream adapters.

	// Other fields.
	logger *zap.Logger // The logger instance to use, defaults to a global logger used by Joe.
}

// Send wraps the given text in a message stanza and sets the recipient to the given channel, which
// is expected to be a JID (bare for direct messages). A error is returned if the channel JID does
// not parse, or if the message fails to send for any reason.
func (c *Client) Send(msg, channel string) error {
	jid, err := jid.Parse(channel)
	if err != nil {
		return fmt.Errorf("parsing JID failed: %w", err)
	}

	// Determine whether this is a direct or group-chat message from the resource part of the JID,
	// which is only set if the message was originally sent as part of a group-chat.
	var kind = stanza.ChatMessage
	if jid.Resourcepart() != "" {
		msg = jid.Resourcepart() + ", " + msg
		jid, kind = jid.Bare(), stanza.GroupChatMessage
	}

	c.logger.Debug("Sending message",
		zap.String("jid", jid.String()),
		zap.String("type", string(kind)),
	)

	return c.session.Send(context.Background(),
		xmlstream.Wrap(
			xmlstream.Wrap(
				xmlstream.Token(xml.CharData(msg)),
				xml.StartElement{Name: xml.Name{Local: "body"}},
			),
			xml.StartElement{
				Name: xml.Name{Local: "message"},
				Attr: []xml.Attr{
					{Name: xml.Name{Local: "id"}, Value: randomID()},
					{Name: xml.Name{Local: "to"}, Value: jid.String()},
					{Name: xml.Name{Local: "type"}, Value: string(kind)},
				},
			},
		),
	)
}

// GroupInfo represents information needed for joining a MUC, either automatically or as part of an
// invite (direct or mediated).
type GroupInfo struct {
	Channel  jid.JID `xml:"-"`
	Password string  `xml:"password"`
	Invite   struct {
		From jid.JID `xml:"from,attr"`
	} `xml:"invite"`
}

// MessageStanza represents an XMPP message stanza, commonly used for transferring chat messages
// among users or group-chats.
type MessageStanza struct {
	// Base, common fields.
	stanza.Message
	Body string `xml:"body"`

	// Additional, optional fields.
	Group GroupInfo `xml:"x"`
}

// HandleInvite responds to the given invite (direct or mediated) with an 'available' presence,
// which allows the client to participate in MUCs.
func (c *Client) handleInvite(w xmlstream.TokenWriter, info *GroupInfo) error {
	if len(c.allowedJIDs) > 0 {
		inviteFrom := info.Invite.From.Bare().String()
		if _, ok := c.allowedJIDs[inviteFrom]; !ok {
			return fmt.Errorf("refusing to handle invite from unknown user: %s", inviteFrom)
		}
	}

	jid, err := info.Channel.WithResource(c.jid.Localpart())
	if err != nil {
		return fmt.Errorf("setting JID for MUC failed: %w", err)
	}

	_, err = xmlstream.Copy(w, xmlstream.Wrap(
		xmlstream.Wrap(
			xmlstream.MultiReader(
				xmlstream.Wrap(
					xmlstream.Token(xml.CharData(info.Password)),
					xml.StartElement{Name: xml.Name{Local: "password"}},
				),
				xmlstream.Wrap(nil, xml.StartElement{
					Name: xml.Name{Local: "history"},
					Attr: []xml.Attr{
						{Name: xml.Name{Local: "maxchars"}, Value: "0"},
					},
				}),
			),
			xml.StartElement{
				Name: xml.Name{Local: "x"},
				Attr: []xml.Attr{
					{Name: xml.Name{Local: "xmlns"}, Value: "http://jabber.org/protocol/muc"},
				},
			},
		),
		stanza.Presence{
			ID:   randomID(),
			Type: stanza.AvailablePresence,
			To:   jid,
		}.StartElement(),
	))

	if err != nil {
		return fmt.Errorf("setting presence for MUC failed: %w", err)
	}

	return nil
}

// HandleMessage parses the given MessageStanza, validating its contents and responding either as a
// direct message, or as a group-chat mention, depending on the intent. HandleMessage will also handle
// invites to group-chats, joining these automatically and with no confirmation needed.
//
// By default, only messages prepended with the local part of the client JID will be responded to in
// group-chats; this is to avoid handling messages where this is not wanted. Such mentions will be,
// in turn, responded to with a mention for the sending user.
//
// Currently, only mediated invites (XEP-0045) are handled, and rooms are not re-joined if the client
// closes its connection to the server.
func (c *Client) handleMessage(w xmlstream.TokenWriter, msg *MessageStanza) error {
	var authorID = msg.From.Bare().String()
	var channel = msg.From.Bare().String()

	switch msg.Type {
	case stanza.GroupChatMessage:
		// Don't handle messages that aren't intended for us.
		n := strings.ToLower(c.jid.Localpart())
		if len(msg.Body) <= len(n) || strings.ToLower(msg.Body[:len(n)]) != n {
			return nil
		}

		channel = msg.From.String()
		msg.Body = strings.Trim(msg.Body[len(n):], " ,:")
		fallthrough
	case stanza.ChatMessage:
		// Do not attempt to handle empty or invalid messages.
		if msg.Body == "" {
			return nil
		}

		if len(c.allowedJIDs) > 0 {
			if _, ok := c.allowedJIDs[authorID]; !ok {
				return fmt.Errorf("refusing to handle message from unknown user: %s", authorID)
			}
		}

		c.emitter.Emit(joe.ReceiveMessageEvent{
			ID:       msg.ID,
			Text:     msg.Body,
			AuthorID: authorID,
			Channel:  channel,
			Data:     msg,
		})
	default:
		// Check if message is a mediated MUC invite, and join MUC if so.
		if !msg.Group.Invite.From.Equal(jid.JID{}) {
			msg.Group.Channel = msg.From.Bare()
			return c.handleInvite(w, &msg.Group)
		}
	}

	return nil
}

// PresenceStanza represents an XMPP presence stanza, commonly used for communicating
// availability.
type PresenceStanza struct {
	// Base, common fields.
	stanza.Presence
}

// HandlePresence parses the given PresenceStanza and responds (usually to the affirmative),
// depending on the presence type, e.g. for subscription requests, HandlePresence will automatically
// subscribe and respond. Any errors returned in parsing on responding will be returned.
func (c *Client) handlePresence(w xmlstream.TokenWriter, p *PresenceStanza) error {
	var err error

	// Handle presence stanza based on type.
	switch p.Type {
	case stanza.SubscribePresence:
		// Respond to subscription requests automatically.
		s := stanza.Presence{ID: randomID(), Type: stanza.SubscribedPresence, To: p.From}
		_, err = xmlstream.Copy(w, s.Wrap(nil))
	}

	if err != nil {
		return err
	}

	return nil
}

// HandleXMPP parses incoming XML tokens and calls a corresponding handler for the stanza type
// represented. Unhandled stanza types will be ignored with no error returned.
func (c *Client) HandleXMPP(t xmlstream.TokenReadEncoder, start *xml.StartElement) error {
	d := xml.NewTokenDecoder(xmlstream.MultiReader(xmlstream.Token(*start), t))
	if _, err := d.Token(); err != nil {
		c.logger.Error("Setting up decoder failed", zap.Error(err))
		return nil
	}

	var s any
	switch start.Name.Local {
	case "message":
		s = &MessageStanza{}
	case "presence":
		s = &PresenceStanza{}
	default:
		c.logger.Debug("Ignoring unknown stanza type", zap.String("type", start.Name.Local))
		return nil // Unknown stanza type, do not handle.
	}

	err := d.DecodeElement(&s, start)
	if err != nil && err != io.EOF {
		c.logger.Error("Decoding element failed", zap.Error(err))
		return nil
	}

	switch start.Name.Local {
	case "message":
		err = c.handleMessage(t, s.(*MessageStanza))
	case "presence":
		err = c.handlePresence(t, s.(*PresenceStanza))
	}

	return c.handleError(t, s, err)
}

// A ErrorReader is any type of stanza that can return an error response for itself.
type errorReader interface {
	Error(stanza.Error) xml.TokenReader
}

// HandleError emits the given error to back to the XMPP server as a response to the given stanza,
// or logs it if emitting failed. It always returns nil, regardless of any failure in emitting the
// error.
func (c *Client) handleError(w xmlstream.TokenWriter, s any, err error) error {
	if err == nil {
		return nil
	}

	var r xml.TokenReader
	if er, ok := s.(errorReader); ok {
		r = er.Error(stanza.Error{
			Type:      stanza.Cancel,
			Condition: stanza.NotAllowed,
			Text:      map[string]string{"": err.Error()},
		})
	} else {
		return nil
	}

	if _, err := xmlstream.Copy(w, r); err != nil {
		c.logger.Error("Handling stanza failed", zap.Error(err))
	}

	return nil
}

// Connect attempts to establish a new XMPP session for the [Client], setting an "available" presence
// if the session is established correctly, or returning an error if not. Successful calls to
// [Client.Connect] are expected to be followed by calls to [Client.Close] in order to ensure that
// any open connections are closed.
func (c *Client) Connect(ctx context.Context) error {
	// Connect to XMPP server as client.
	conn, err := c.dialer.Dial(ctx, "tcp", c.jid)
	if err != nil {
		return fmt.Errorf("establishing connection failed: %w", err)
	}

	s, err := xmpp.NewClientSession(ctx, c.jid, conn, c.features...)
	if err != nil {
		return fmt.Errorf("establishing session failed: %w", err)
	}

	c.session = s

	// Send initial presence to let the server know we want to receive messages.
	err = c.session.Send(ctx, stanza.Presence{Type: stanza.AvailablePresence}.Wrap(nil))
	if err != nil {
		return fmt.Errorf("setting initial presence failed: %w", err)
	}

	return nil
}

// Serve initiates handling of XML tokens over a XMPP server connection, as established by [Connect].
// Calls to this function will return an error if the session has not been established.
func (c *Client) Serve(ctx context.Context) error {
	if c.session == nil {
		return fmt.Errorf("cannot serve for inactive session connection")
	}

	return c.session.Serve(c)
}

// Close shuts down the active XMPP session and server connection, returning an error if the process
// fails at any point.
func (c *Client) Close() error {
	if c.session == nil {
		return nil
	}

	if err := c.session.Close(); err != nil {
		return err
	}

	type connSession interface{ Conn() net.Conn }
	if s, ok := c.session.(connSession); ok {
		if err := s.Conn().Close(); err != nil {
			return err
		}
	}

	c.session = nil
	return nil
}

// RegisterAt sets the [joe.Brain] instance for the XMPP client.
func (c *Client) RegisterAt(brain *joe.Brain) {
	c.emitter = brain
}

// NewClient returns an XMPP client that is ready to connect to an XMPP server for the given
// configuration.
func NewClient(conf Config) (*Client, error) {
	id, err := jid.Parse(conf.JID)
	if err != nil {
		return nil, fmt.Errorf("parsing client JID failed: %w", err)
	}

	var c = &Client{
		jid:      id,
		dialer:   &dial.Dialer{NoTLS: conf.NoTLS},
		features: []xmpp.StreamFeature{xmpp.BindResource()},
		logger:   conf.Logger,
	}

	var tlsConfig = &tls.Config{
		ServerName:         id.Domain().String(),
		InsecureSkipVerify: conf.NoVerifyTLS, //nolint:gosec // This is required for local development.
	}

	if conf.NoVerifyTLS {
		c.dialer.(*dial.Dialer).TLSConfig = tlsConfig
	}

	if conf.UseStartTLS {
		c.features = append(c.features, xmpp.StartTLS(tlsConfig))
	}

	if conf.Password != "" {
		c.features = append(c.features, xmpp.SASL("", conf.Password, defaultAuthMechanisms...))
	}

	if conf.AllowedJIDs != "" {
		c.allowedJIDs = make(map[string]struct{})
		for id := range strings.FieldsSeq(conf.AllowedJIDs) {
			if _, err := jid.Parse(id); err != nil {
				return nil, fmt.Errorf("parsing allowed JID failed: %w", err)
			}
			c.allowedJIDs[id] = struct{}{}
		}
	}

	return c, nil
}

const (
	// The default amount of time we'll attempt to wait before re-establishing an XMPP session.
	sessionRetryWait = 5 * time.Second

	// The maximum amount of time we'll wait before re-establishing an XMPP session when backing-off
	// incrementally.
	sessionRetryWaitMax = 1 * time.Minute
)

// Adapter initializes an XMPP client connection according to configuration given, and returns a Joe
// module, usable in calls to joe.New(), or an error if any occurs.
func Adapter(ctx context.Context, conf Config) joe.Module {
	return joe.ModuleFunc(func(joeConf *joe.Config) error {
		c, err := NewClient(conf)
		if err != nil {
			return err
		}

		if c.logger == nil {
			c.logger = joeConf.Logger("xmpp")
		}

		if err = c.Connect(ctx); err != nil {
			return err
		}

		var sessionRetryCount int
		go func() {
			for {
				err := c.session.Serve(c)
				switch {
				case errors.Is(err, net.ErrClosed):
					return
				case err != nil:
					waitTime := min(sessionRetryWait*time.Duration(sessionRetryCount), sessionRetryWaitMax)
					c.logger.Error("client session error, retrying", zap.Error(err), zap.Duration("wait", waitTime))
					if waitTime > 0 {
						time.Sleep(waitTime)
					}
					if err = c.Close(); err != nil {
						c.logger.Error("error closing client session for re-try", zap.Error(err))
					}
					if err = c.Connect(ctx); err != nil {
						c.logger.Error("error re-connecting client session", zap.Error(err))
					}
					sessionRetryCount += 1
				}
			}
		}()

		joeConf.SetAdapter(c)
		return nil
	})
}

// RandomID returns a cryptographically secure, 16-byte random string, useful for adding to stanzas
// for uniquely identifying them.
func randomID() string {
	var buf = make([]byte, 16)
	if _, err := rand.Reader.Read(buf); err != nil {
		panic("randomID: " + err.Error())
	}

	return fmt.Sprintf("%x", buf)[:16]
}
