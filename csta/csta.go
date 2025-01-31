package csta

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"log/syslog"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

type Client struct {
	conn        *websocket.Conn
	mxIP        string
	session     string
	invokeID    int
	mu          sync.Mutex
	isConnected bool
	handlers    map[string]MessageHandler
	handlersMu  sync.RWMutex
}

type MessageHandler func(message json.RawMessage) error

// LoginResponse Response types
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
	}
}

func (c *Client) Connect() error {
	url := fmt.Sprintf("wss://%s/cstaj", c.mxIP)
	conn, _, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		return fmt.Errorf("dial error: %w", err)
	}
	c.conn = conn
	c.isConnected = true

	// Register default handlers
	c.RegisterHandler("syslog", c.handleSyslog)
	c.RegisterHandler("updateUser", c.handleUserUpdate)

	// Start message handler
	go c.handleMessages()

	// Send login message
	if err := c.login(); err != nil {
		return fmt.Errorf("login failed: %w", err)
	}

	// Start keepalive routine
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

func (c *Client) sendMessage(msg interface{}) error {
	jsonBytes, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("marshal error: %w", err)
	}

	serialized := c.serializeMessage(len(jsonBytes)) + string(jsonBytes)
	return c.conn.WriteMessage(websocket.TextMessage, []byte(serialized))
}

func (c *Client) keepAlive() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for range ticker.C {
		if !c.isConnected {
			return
		}
		msg := struct {
			Command string `json:"command"`
		}{
			Command: "keepalive",
		}
		if err := c.sendMessage(msg); err != nil {
			log.Printf("keepalive error: %v", err)
			return
		}
	}
}

func (c *Client) handleMessages() {
	for {
		if !c.isConnected {
			return
		}

		_, message, err := c.conn.ReadMessage()
		if err != nil {
			log.Printf("read error: %v", err)
			return
		}

		//log.Printf("received message: %s", message)

		if len(message) < 2 {
			continue
		}

		// Get the base64 part length from first byte
		headerLen := int(message[0])
		if len(message) < headerLen+1 { // +1 for the length byte itself
			continue
		}

		// Extract the JSON content (everything after header)
		jsonContent := message[headerLen+1:]

		// Parse the message to check for command
		var msg struct {
			Command string          `json:"command"`
			Data    json.RawMessage `json:"data,omitempty"`
		}

		if err := json.Unmarshal(jsonContent, &msg); err != nil {
			log.Printf("json unmarshal error: %v", err)
			continue
		}

		// Handle messages with command
		if msg.Command != "" {
			c.handlersMu.RLock()
			handler, exists := c.handlers[msg.Command]
			c.handlersMu.RUnlock()

			if exists {
				if err := handler(jsonContent); err != nil {
					log.Printf("handler error for command %s: %v", msg.Command, err)
				}
			}
		} else {
			// Check for activityClientList structure
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
}

func (c *Client) RegisterHandler(command string, handler MessageHandler) {
	c.handlersMu.Lock()
	defer c.handlersMu.Unlock()
	c.handlers[command] = handler
	log.Printf("[%s] %s: %s",
		syslog.LOG_DEBUG,
		time.Now().Unix(),
		"Registering handler for "+command)
}

func (c *Client) handleSyslog(data json.RawMessage) error {
	var syslog SyslogMessage
	if err := json.Unmarshal(data, &syslog); err != nil {
		return fmt.Errorf("failed to parse syslog message: %w", err)
	}
	log.Printf("[%s] %s: %s",
		syslog.Severity,
		time.Unix(syslog.EventTimeStamp, 0),
		syslog.Message)
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
		log.Printf("User updated: %s %s (ID: %s)",
			user.FirstName,
			user.LastName,
			user.UserID)
	}
	return nil
}

func (c *Client) Close() error {
	c.isConnected = false
	if c.conn != nil {
		return c.conn.Close()
	}
	return nil
}
