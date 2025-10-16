// File: chat/client.go
package chat_csta

import (
	"fmt"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

type ChatClient struct {
	conn        *websocket.Conn
	toolkit     *CSTAToolkit
	mu          sync.RWMutex
	roomID      string
	agent       string
	chatState   string
	persistID   string
	customName  string
	chatActive  bool
	typingTimer *time.Timer

	// Callbacks
	OnMessage     func(message ChatMessage)
	OnStateChange func(state string)
	OnTyping      func(agent string, isTyping bool)
	OnError       func(err error)
	OnAgentJoined func(agent string)
	OnAgentLeft   func(agent string)
	OnDelivered   func(messageID string)
	OnSeen        func(messageID string)
}

type ChatMessage struct {
	ID        string    `json:"id"`
	Text      string    `json:"text"`
	From      string    `json:"from"`
	FromName  string    `json:"fromName"`
	Timestamp time.Time `json:"timestamp"`
	Type      string    `json:"type"`
}

type Config struct {
	ServerAddress string
	GroupName     string
	CustomName    string
}

func NewChatClient(config Config) *ChatClient {
	client := &ChatClient{
		toolkit:    NewCSTAToolkit(),
		customName: config.CustomName,
		chatActive: true,
	}

	client.setupEventHandlers()
	return client
}

func (c *ChatClient) setupEventHandlers() {
	c.toolkit.DeliveredHandler = func(event DeliveredEvent) {
		if c.OnDelivered != nil {
			c.OnDelivered(event.CallID)
		}
	}

	c.toolkit.ErrorHandler = func(event ErrorEvent) {
		if c.OnError != nil {
			c.OnError(fmt.Errorf(event.Message))
		}
	}

	c.toolkit.SetMessageHandlers(
		// ChatMessage handler
		func(msg Message) {
			message := ChatMessage{
				ID:        msg.MsgID,
				Text:      msg.Text,
				From:      msg.From,
				FromName:  msg.Name,
				Type:      "in",
				Timestamp: time.Now(),
			}
			if c.OnMessage != nil {
				c.OnMessage(message)
			}
		},
		// ChatMessage Ack handler
		func(ack MessageAck) {
			switch ack.Code {
			case "IMS_WC_ACCEPTED":
				c.mu.Lock()
				if ack.WebChat != nil {
					c.roomID = ack.WebChat.RoomID
				}
				c.agent = ack.Name
				c.chatState = "connected"
				c.mu.Unlock()

				if c.OnStateChange != nil {
					c.OnStateChange("connected")
				}
				if c.OnAgentJoined != nil {
					c.OnAgentJoined(ack.Name)
				}

			case "IMS_WC_CANCELLED", "IMS_WC_DECLINED":
				c.mu.Lock()
				c.chatState = ack.Code
				c.mu.Unlock()

				if c.OnStateChange != nil {
					c.OnStateChange(ack.Code)
				}
				if c.OnAgentLeft != nil {
					c.OnAgentLeft(ack.Name)
				}

			case "IMS_TYPING":

				if c.OnTyping != nil && ack.Typing != nil {
					c.OnTyping(ack.Name, ack.Typing.ActionStart == "true")
				}

			case "IMS_DELIVERED":
				if c.OnDelivered != nil {
					c.OnDelivered(ack.MsgID)
				}

			case "IMS_SEEN":
				if c.OnSeen != nil {
					c.OnSeen(ack.MsgID)
				}
			}
		},
	)
}

func (c *ChatClient) LoginZAC(config Config) error {
	dialer := websocket.Dialer{
		HandshakeTimeout: 10 * time.Second,
	}

	conn, _, err := dialer.Dial(fmt.Sprintf("wss://%s:7779", config.ServerAddress), nil)
	if err != nil {
		return fmt.Errorf("failed to connect: %v", err)
	}

	c.mu.Lock()
	c.conn = conn
	c.chatState = "connecting"
	c.mu.Unlock()

	if c.OnStateChange != nil {
		c.OnStateChange("connecting")
	}

	// Start session
	loginPacket := c.toolkit.CreateLogin("200", "P@ssw0rd", "1.2.3", false, false)
	err = c.sendWebSocketMessage(loginPacket)
	if err != nil {
		return err
	}

	// Start message reader
	go c.readMessages()

	return nil
}

func (c *ChatClient) Connect(config Config) error {
	dialer := websocket.Dialer{
		HandshakeTimeout: 10 * time.Second,
	}

	conn, _, err := dialer.Dial(fmt.Sprintf("wss://%s:7779", config.ServerAddress), nil)
	if err != nil {
		return fmt.Errorf("failed to connect: %v", err)
	}

	c.mu.Lock()
	c.conn = conn
	c.chatState = "connecting"
	c.mu.Unlock()

	if c.OnStateChange != nil {
		c.OnStateChange("connecting")
	}

	// Start session
	err = c.startSession(config.GroupName)
	if err != nil {
		return fmt.Errorf("failed to start session: %v", err)
	}

	// Start message reader
	go c.readMessages()

	return nil
}

func (c *ChatClient) sendWebSocketMessage(message string) error {
	c.mu.RLock()
	conn := c.conn
	c.mu.RUnlock()

	if conn == nil {
		return fmt.Errorf("not connected")
	}

	return conn.WriteMessage(websocket.TextMessage, []byte(message))
}

func (c *ChatClient) SendMessage(text string) error {
	c.mu.RLock()
	roomID := c.roomID
	name := c.customName
	c.mu.RUnlock()

	/*if roomID == "" {
		return fmt.Errorf("not connected to a chat room")
	}*/

	msg := c.toolkit.CreateWebChatMsgNew(text, roomID, name)
	err := c.sendWebSocketMessage(msg)
	if err != nil {
		return fmt.Errorf("failed to send message: %v", err)
	}

	message := ChatMessage{
		Text:      text,
		From:      c.customName,
		FromName:  c.customName,
		Timestamp: time.Now(),
		Type:      "out",
	}

	if c.OnMessage != nil {
		c.OnMessage(message)
	}

	return nil
}

func (c *ChatClient) StartTyping() {
	c.mu.RLock()
	roomID := c.roomID
	name := c.customName
	persistID := c.persistID
	c.mu.RUnlock()

	if c.typingTimer != nil {
		c.typingTimer.Stop()
	}

	status := c.toolkit.CreateImMsgAckStatus(
		"IMS_TYPING",
		"",
		persistID,
		"",
		"",
		"WebChat",
		"",
		name,
		roomID,
		time.Now().Unix(),
		"true",
		"",
	)

	_ = c.sendWebSocketMessage(status)

	c.typingTimer = time.AfterFunc(500*time.Millisecond, func() {
		c.StopTyping()
	})
}

func (c *ChatClient) StopTyping() {
	if c.typingTimer != nil {
		c.typingTimer.Stop()
		c.typingTimer = nil
	}

	c.mu.RLock()
	roomID := c.roomID
	name := c.customName
	persistID := c.persistID
	c.mu.RUnlock()

	status := c.toolkit.CreateImMsgAckStatus(
		"IMS_TYPING",
		"",
		persistID,
		"",
		"",
		"WebChat",
		"",
		name,
		roomID,
		time.Now().Unix(),
		"false",
		"",
	)

	_ = c.sendWebSocketMessage(status)
}

func (c *ChatClient) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.conn != nil {
		c.chatState = "closed"
		if c.OnStateChange != nil {
			c.OnStateChange("closed")
		}
		return c.conn.Close()
	}
	return nil
}

func (c *ChatClient) readMessages() {
	for {
		c.mu.RLock()
		conn := c.conn
		c.mu.RUnlock()

		if conn == nil {
			return
		}

		_, message, err := conn.ReadMessage()
		if err != nil {
			if c.OnError != nil {
				c.OnError(fmt.Errorf("read error: %v", err))
			}
			return
		}

		err = c.toolkit.ParseData(string(message))
		if err != nil && c.OnError != nil {
			c.OnError(fmt.Errorf("parse error: %v", err))
		}
	}
}

func (c *ChatClient) startSession(groupName string) error {
	c.mu.RLock()
	conn := c.conn
	name := c.customName
	c.mu.RUnlock()

	if conn == nil {
		return fmt.Errorf("not connected")
	}

	err := conn.WriteMessage(websocket.TextMessage, []byte(`{"session_id":""}`))
	if err != nil {
		return fmt.Errorf("failed to send session message: %v", err)
	}

	pack := c.toolkit.CreateCommunication(
		"IMS_WC_REQUEST",
		"0",
		"0",
		"",
		"WebChat",
		groupName,
		name,
	)

	err = conn.WriteMessage(websocket.TextMessage, []byte(pack))
	if err != nil {
		return fmt.Errorf("failed to send communication packet: %v", err)
	}

	return nil
}
