package admin_csta

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
)

type MessageHandler func(message json.RawMessage) error

type Client struct {
	conn    *websocket.Conn
	mxIP    string
	session string

	// invokeID + header serialization lock
	invokeID int
	mu       sync.Mutex

	// serialize all websocket writes (gorilla requires single writer)
	writeMu sync.Mutex

	isConnected atomic.Bool

	handlers   map[string]MessageHandler
	handlersMu sync.RWMutex

	// shutdown & error signaling
	done      chan struct{} // closed exactly once to broadcast shutdown
	closeOnce sync.Once
	errCh     chan error // buffered; first fatal error wins
}

type LoginResponse struct {
	Index         int `json:"index"`
	LoginResponse struct {
		APIVersion      string `json:"apiVersion"`
		Code            string `json:"code"`
		Ext             string `json:"ext"`
		PairedNode      string `json:"pairedNode"`
		SN              string `json:"sn"`
		SwitchoverDelay int    `json:"switchoverDelay"`
		UserID          string `json:"userId"`
		WWWUUID         string `json:"wwwUuid"`
	} `json:"loginResponse"`
	Status  string `json:"status"`
	Success bool   `json:"success"`
}

type UserData struct {
	FirstName string `json:"firstName"`
	LastName  string `json:"lastName"`
	Login     string `json:"login"`
	ProfileID string `json:"profileId"`
	Pseudonym string `json:"pseudonym"`
	UniqueID  string `json:"uniqueId"`
	UserID    string `json:"userId"`
}

type SyslogMessage struct {
	Command        string `json:"command"`
	EventTimeStamp int64  `json:"eventTimeStamp"`
	ID             int64  `json:"id"`
	Message        string `json:"message"`
	Severity       string `json:"severity"`
}

func NewCSTAClient(mxIP, session string) *Client {
	return &Client{
		mxIP:     mxIP,
		session:  session,
		invokeID: 0,
		handlers: make(map[string]MessageHandler),
		done:     make(chan struct{}),
		errCh:    make(chan error, 1),
	}
}

// Err returns a channel that receives a fatal error when the client connection fails.
// Typical usage in a supervisor: <-client.Err()
func (c *Client) Err() <-chan error {
	return c.errCh
}

func (c *Client) Connect() error {
	url := fmt.Sprintf("wss://%s/cstaj", c.mxIP)

	conn, _, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		return fmt.Errorf("dial error: %w", err)
	}
	c.conn = conn
	c.isConnected.Store(true)

	// ---- WebSocket liveness settings ----
	const (
		pongWait   = 60 * time.Second // how long we wait for the next pong / message
		pingPeriod = 20 * time.Second // send ping < pongWait
	)
	c.conn.SetReadLimit(1 << 20)
	_ = c.conn.SetReadDeadline(time.Now().Add(pongWait))
	c.conn.SetPongHandler(func(string) error {
		// any pong extends our read deadline
		return c.conn.SetReadDeadline(time.Now().Add(pongWait))
	})

	// Ping loop (control frames). Separate from app-level keepalive messages.
	go func() {
		t := time.NewTicker(pingPeriod)
		defer t.Stop()
		for {
			select {
			case <-c.done:
				return
			case <-t.C:
				c.writeMu.Lock()
				err := c.conn.WriteControl(websocket.PingMessage, nil, time.Now().Add(5*time.Second))
				c.writeMu.Unlock()
				if err != nil {
					log.Printf("ping error: %v", err)
					c.fail(err)
					return
				}
			}
		}
	}()

	// Register default handlers
	c.RegisterHandler("syslog", c.handleSyslog)
	c.RegisterHandler("updateUser", c.handleUserUpdate)

	// Start reader
	go c.handleMessages()

	// Login
	if err := c.login(); err != nil {
		return fmt.Errorf("login failed: %w", err)
	}

	// Start app-level keepalive messages
	go c.keepAlive()

	return nil
}

func (c *Client) serializeMessage(msgLength int) string {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Create an 8-byte header
	header := make([]byte, 8)

	// First 2 bytes are 0x00
	header[0] = 0x00
	header[1] = 0x00

	// Next 2 bytes are the message length + 8
	totalLen := msgLength + 8
	header[2] = byte(totalLen >> 8)   // high byte
	header[3] = byte(totalLen & 0xFF) // low byte

	// Last 4 bytes are the ASCII characters of invokeId
	invokeStr := fmt.Sprintf("%04d", c.invokeID)
	copy(header[4:], []byte(invokeStr))

	// Base64 encode the header
	base64Header := base64.StdEncoding.EncodeToString(header)

	// Create the final header with length byte
	headerLen := byte(len(base64Header))

	// Increment invokeId and wrap at 9999
	c.invokeID++
	if c.invokeID == 10000 {
		c.invokeID = 0
	}

	return string(headerLen) + base64Header
}

func (c *Client) login() error {
	msg := struct {
		Command    string `json:"command"`
		Type       string `json:"type"`
		Platform   string `json:"platform"`
		Version    string `json:"version"`
		ClientType string `json:"clientType"`
		Forced     bool   `json:"forced"`
		DCMode     string `json:"dcmode"`
		Session    string `json:"session"`
	}{
		Command:    "loginRequest",
		Type:       "user",
		Platform:   "admin",
		Version:    "1.2.2",
		ClientType: "web",
		Forced:     true,
		DCMode:     "phone",
		Session:    c.session,
	}

	return c.sendMessage(msg)
}

func (c *Client) SubscribeToAdmin(notifyType string) error {
	msg := struct {
		Command string `json:"command"`
		Type    string `json:"type,omitempty"`
	}{
		Command: "subscribeToAdmin",
		Type:    notifyType,
	}

	return c.sendMessage(msg)
}

// sendMessage marshals and writes a text message with serialized header.
// IMPORTANT: all writes are serialized with writeMu (gorilla single-writer rule).
func (c *Client) sendMessage(msg interface{}) error {
	jsonBytes, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("marshal error: %w", err)
	}
	serialized := c.serializeMessage(len(jsonBytes)) + string(jsonBytes)

	c.writeMu.Lock()
	defer c.writeMu.Unlock()

	if !c.isConnected.Load() {
		return fmt.Errorf("connection closed")
	}
	return c.conn.WriteMessage(websocket.TextMessage, []byte(serialized))
}

// keepAlive sends periodic app-level keepalive messages.
// It exits on c.done or on first write error (and triggers fail()).
func (c *Client) keepAlive() {
	ticker := time.NewTicker(25 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-c.done:
			return
		case <-ticker.C:
			msg := struct {
				Command string `json:"command"`
			}{Command: "keepalive"}
			if err := c.sendMessage(msg); err != nil {
				log.Printf("keepalive error: %v", err)
				c.fail(err)
				return
			}
		}
	}
}

// handleMessages reads frames and dispatches by "command" or special shapes.
// On read error it triggers fail() and returns.
func (c *Client) handleMessages() {
	for {
		if !c.isConnected.Load() {
			return
		}

		_, message, err := c.conn.ReadMessage()
		if err != nil {
			log.Printf("read error: %v", err)
			c.fail(err)
			return
		}

		// Expect first byte = base64 header length (as a single byte)
		if len(message) < 2 {
			continue
		}
		headerLen := int(message[0])
		if len(message) < headerLen+1 { // +1 for the length byte
			continue
		}

		// Extract pure JSON payload after the header
		jsonContent := message[headerLen+1:]

		var envelope struct {
			Command string          `json:"command"`
			Data    json.RawMessage `json:"data,omitempty"`
		}
		if err := json.Unmarshal(jsonContent, &envelope); err != nil {
			log.Printf("json unmarshal error: %v", err)
			continue
		}

		// Dispatch
		if envelope.Command != "" {
			c.handlersMu.RLock()
			handler, exists := c.handlers[envelope.Command]
			c.handlersMu.RUnlock()

			if exists {
				payload := envelope.Data
				if len(payload) == 0 {
					// some events might omit "data" and put everything flat
					payload = jsonContent
				}
				if err := handler(payload); err != nil {
					log.Printf("handler error for command %s: %v", envelope.Command, err)
				}
			}
			continue
		}

		// Fallback: detect shapes like {"activityClientList": ...}
		var raw map[string]json.RawMessage
		if err := json.Unmarshal(jsonContent, &raw); err == nil {
			if _, ok := raw["activityClientList"]; ok {
				c.handlersMu.RLock()
				handler, exists := c.handlers["activityClient"]
				c.handlersMu.RUnlock()
				if exists {
					if err := handler(jsonContent); err != nil {
						log.Printf("handler error for activityClient: %v", err)
					}
				}
			}
		}
	}
}

// RegisterHandler registers a command handler.
func (c *Client) RegisterHandler(command string, handler MessageHandler) {
	c.handlersMu.Lock()
	defer c.handlersMu.Unlock()
	c.handlers[command] = handler
	log.Printf("[DEBUG] %d: Registering handler for %s", time.Now().Unix(), command)
}

func (c *Client) handleSyslog(data json.RawMessage) error {
	var s SyslogMessage
	if err := json.Unmarshal(data, &s); err != nil {
		return fmt.Errorf("failed to parse syslog message: %w", err)
	}
	log.Printf("[%s] %s: %s",
		s.Severity,
		time.Unix(s.EventTimeStamp, 0).Format(time.RFC3339),
		s.Message)
	return nil
}

func (c *Client) handleUserUpdate(data json.RawMessage) error {
	var msg struct {
		Data []UserData `json:"data"`
	}
	if err := json.Unmarshal(data, &msg); err != nil {
		return fmt.Errorf("failed to parse user update: %w", err)
	}
	for _, user := range msg.Data {
		log.Printf("User updated: %s %s (ID: %s)", user.FirstName, user.LastName, user.UserID)
	}
	return nil
}

// Close is idempotent and signals all loops to exit, then closes the websocket.
func (c *Client) Close() error {
	c.closeOnce.Do(func() {
		c.isConnected.Store(false)
		close(c.done)
		// best-effort close under write lock to avoid racing with writers
		c.writeMu.Lock()
		defer c.writeMu.Unlock()
		if c.conn != nil {
			_ = c.conn.Close()
		}
	})
	return nil
}

// fail closes the client and reports a fatal error once.
func (c *Client) fail(err error) {
	if err == nil {
		err = fmt.Errorf("unknown client error")
	}
	_ = c.Close()
	select {
	case c.errCh <- err:
	default:
		// error already reported
	}
}

// IsConnected returns true if the websocket is open.
func (c *Client) IsConnected() bool {
	return c.isConnected.Load()
}
