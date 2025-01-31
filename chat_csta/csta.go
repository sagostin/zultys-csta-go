package chat_csta

import (
	"encoding/base64"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"strings"
	"sync"
)

// EventType represents different types of CSTA events
type EventType int

const (
	EventDelivered EventType = iota
	EventEstablished
	EventCleared
	EventParked
	EventHeld
	EventRetrieved
	EventAssigned
	EventPresence
	EventCSTAError
	EventNetworkError
	EventCallLog
	EventWCStatus
	EventWCConfig
	EventWCPartner
	EventWCMsg
	EventAgentDeleted
	EventAgentAdded
	EventIMMsg
	EventIMMsgAck
	EventWCAgentAdded
	EventWCAgentDeleted
	EventUnknown
)

// BindType represents different types of device binding
type BindType int

const (
	BindCurrent BindType = iota
	BindDeviceID
	BindExternal
)

// AgentLoginState represents the login state of an agent
type AgentLoginState int

const (
	AgentLoggedOn AgentLoginState = iota
	AgentLoggedOff
)

// CSTAToolkit provides the main functionality for CSTA operations
type CSTAToolkit struct {
	invokeID   int
	callbacks  map[string]interface{}
	callbackMu sync.RWMutex

	MessageHandler    func(msg Message)
	MessageAckHandler func(ack MessageAck)

	// Event handlers
	DeliveredHandler   func(event DeliveredEvent)
	EstablishedHandler func(event EstablishedEvent)
	ClearedHandler     func(event ClearedEvent)
	ParkedHandler      func(event ParkedEvent)
	/*HeldHandler         func(event HeldEvent)
	RetrievedHandler    func(event RetrievedEvent)
	AssignedHandler     func(event AssignedEvent)*/
	PresenceHandler     func(event PresenceEvent)
	ErrorHandler        func(event ErrorEvent)
	NetworkErrorHandler func(event NetworkErrorEvent)
}

// Event structures
type DeliveredEvent struct {
	MonitorID             string
	CallID                string
	DeviceID              string
	AlertingDevice        string
	AlertingDisplayName   string
	NetworkCallingDevice  string
	CallingDevice         string
	CalledDevice          string
	LastRedirectionDevice string
	LocalConnectionInfo   string
	Cause                 string
}

type EstablishedEvent struct {
	MonitorID             string
	CallID                string
	DeviceID              string
	AnsweringDevice       string
	AnsweringDisplayName  string
	CallingDisplayName    string
	CallingDevice         string
	CalledDevice          string
	LastRedirectionDevice string
	Cause                 string
}

type ClearedEvent struct {
	MonitorID           string
	CallID              string
	DeviceID            string
	ReleasingDevice     string
	LocalConnectionInfo string
	Cause               string
}

type ParkedEvent struct {
	MonitorID string
	ParkID    string
}

type PresenceEvent struct {
	From   string
	Status string
}

type ErrorEvent struct {
	Message string
}

type NetworkErrorEvent struct {
	Message string
}

// NewCSTAToolkit creates a new instance of CSTAToolkit
func NewCSTAToolkit() *CSTAToolkit {
	return &CSTAToolkit{
		callbacks: make(map[string]interface{}),
	}
}

// XML message creation methods
func (c *CSTAToolkit) CreateLogin(login, password, version string, isPlain, forced bool) string {
	request := fmt.Sprintf(`<loginRequest type='User' platform='Salesforce' version='%s' clientType='CRM' loginCapab='Audio' forced='%v'>
		<userName>%s</userName>
		<pwd>%s</pwd>
	</loginRequest>`, version, forced, login, password)

	header := c.serializeHeader(len(request))
	return header + request
}

func (c *CSTAToolkit) CreateLogout() string {
	data := "<logout></logout>"
	header := c.serializeHeader(len(data))
	return header + data
}

func (c *CSTAToolkit) CreateWebSession(roomID, name string) string {
	data := fmt.Sprintf(`<?xml version="1.0" encoding="utf-8"?>
		<loginRequest type="User" platform="ExtWeb" forced='true' version="1.0" abNotify="true" 
		dispName="%s" clientType="Web" loginCapab="Im|WebChat|ScreenSharing" apiVersion="17">
		<userName>webuser_%s</userName>
		</loginRequest>`, escapeXML(name), roomID)

	header := c.serializeHeader(len(data))
	return header + data
}

// Helper methods
func (c *CSTAToolkit) serializeHeader(dataLength int) string {
	header := make([]byte, 8)
	header[0] = 0
	header[1] = 0

	dataLen := dataLength + 8
	header[2] = byte(dataLen / 256)
	header[3] = byte(dataLen % 256)

	invokeIDStr := fmt.Sprintf("%04d", c.invokeID)
	copy(header[4:], invokeIDStr)

	headerB64 := base64.StdEncoding.EncodeToString(header)
	encodedHeader := string(byte(len(headerB64))) + headerB64

	c.invokeID = (c.invokeID + 1) % 9999
	return encodedHeader
}

func escapeXML(unsafe string) string {
	if unsafe == "" {
		return ""
	}

	replacer := strings.NewReplacer(
		"<", "&lt;",
		">", "&gt;",
		"&", "&amp;",
		"'", "&apos;",
		"\"", "&quot;",
	)

	return replacer.Replace(unsafe)
}

type SessionResponse struct {
	SessionValid bool   `json:"session_valid"`
	SessionID    string `json:"session_id"`
}

func (c *CSTAToolkit) ParseData(message string) error {
	// Check if it's a JSON message (starts with {)
	if strings.TrimSpace(message)[0] == '{' {
		var session SessionResponse
		if err := json.Unmarshal([]byte(message), &session); err != nil {
			return fmt.Errorf("failed to parse session response: %v", err)
		}
		// Handle session response if needed
		return nil
	}

	// Handle XML format
	if len(message) < 1 {
		return fmt.Errorf("message too short")
	}

	encodedLength := int(message[0])
	if len(message) < 1+encodedLength {
		return fmt.Errorf("invalid message length")
	}

	encodedHeader := message[1 : 1+encodedLength]
	rawData := message[1+encodedLength:]

	decodedHeader, err := base64.StdEncoding.DecodeString(encodedHeader)
	if err != nil {
		return fmt.Errorf("failed to decode header: %v", err)
	}

	invokeID := string(decodedHeader[4:8])
	if invokeID == "9999" {
		return c.processEvent(rawData)
	}

	return c.processResponse(invokeID, rawData)
}

/*func (c *CSTAToolkit) detectEventType(message string) EventType {
	switch {
	case strings.Contains(message, "DeliveredEvent"):
		return EventDelivered
	case strings.Contains(message, "EstablishedEvent"):
		return EventEstablished
	case strings.Contains(message, "ConnectionClearedEvent"):
		return EventCleared
	// Add other event type detections here
	case strings.Contains(message, "<message>"):
		return EventIMMsg
	case strings.Contains(message, "<messageAck>"):
		return EventIMMsgAck
	default:
		return EventUnknown
	}
}*/

func (c *CSTAToolkit) detectEventType(message string) EventType {
	// Helper function to check XML tags
	hasTag := func(tag string) bool {
		return strings.Contains(message, "<"+tag)
	}

	switch {
	case hasTag("message "):
		return EventIMMsg
	case hasTag("messageAck"):
		return EventIMMsgAck
	case hasTag("DeliveredEvent"):
		return EventDelivered
	case hasTag("EstablishedEvent"):
		return EventEstablished
	case hasTag("ConnectionClearedEvent"):
		return EventCleared
	case hasTag("CSTAErrorCode"):
		return EventCSTAError
	case hasTag("presence"):
		return EventPresence
	case hasTag("webChatStatusEvent"):
		return EventWCStatus
	case hasTag("webChatConfigEvent"):
		return EventWCConfig
	case hasTag("WcUserAdded"):
		return EventWCAgentAdded
	case hasTag("WcUserRemoved"):
		return EventWCAgentDeleted
	default:
		fmt.Printf("Unhandled message type: %s\n", message) // For debugging
		return EventUnknown
	}
}

func (c *CSTAToolkit) processEvent(message string) error {
	eventType := c.detectEventType(message)

	switch eventType {
	case EventIMMsg:
		if c.MessageHandler != nil {
			var msg Message
			if err := xml.Unmarshal([]byte(message), &msg); err != nil {
				return fmt.Errorf("failed to unmarshal IM message: %v", err)
			}
			// Debug logging
			fmt.Printf("Received message from %s: %s\n", msg.From, msg.Text)
			c.MessageHandler(msg)
		}

	case EventIMMsgAck:
		if c.MessageAckHandler != nil {
			var ack MessageAck
			if err := xml.Unmarshal([]byte(message), &ack); err != nil {
				return fmt.Errorf("failed to unmarshal message ack: %v", err)
			}
			// Debug logging
			fmt.Printf("Received ack with code: %s\n", ack.Code)
			c.MessageAckHandler(ack)
		}

	case EventCSTAError:
		if c.ErrorHandler != nil {
			var errorEvent ErrorEvent
			errorEvent.Message = message // You might want to parse this more specifically
			c.ErrorHandler(errorEvent)
		}

	case EventWCAgentAdded:
		if c.MessageAckHandler != nil {
			ack := MessageAck{
				Code: "IMS_WC_AGENT_ADDED",
				// Parse the XML to get agent details
			}
			c.MessageAckHandler(ack)
		}

	case EventWCAgentDeleted:
		if c.MessageAckHandler != nil {
			ack := MessageAck{
				Code: "IMS_WC_AGENT_DELETED",
				// Parse the XML to get agent details
			}
			c.MessageAckHandler(ack)
		}

	// Add other cases as needed

	default:
		return fmt.Errorf("unhandled message type for: %s", message)
	}

	return nil
}

/*func (c *CSTAToolkit) processEvent(message string) error {
	eventType := c.detectEventType(message)

	switch eventType {
	case EventDelivered:
		if c.DeliveredHandler != nil {
			event, err := parseDeliveredEvent(message)
			if err != nil {
				return err
			}
			c.DeliveredHandler(event)
		}
	case EventEstablished:
		if c.EstablishedHandler != nil {
			event, err := parseEstablishedEvent(message)
			if err != nil {
				return err
			}
			c.EstablishedHandler(event)
		}
	case EventIMMsg:
		if c.MessageHandler != nil {
			var msg Message
			if err := xml.Unmarshal([]byte(message), &msg); err != nil {
				return fmt.Errorf("failed to unmarshal IM message: %v", err)
			}
			c.MessageHandler(msg)
		}
	case EventIMMsgAck:
		if c.MessageAckHandler != nil {
			var ack MessageAck
			if err := xml.Unmarshal([]byte(message), &ack); err != nil {
				return fmt.Errorf("failed to unmarshal message ack: %v", err)
			}
			c.MessageAckHandler(ack)
		}
	default:
		return errors.New("unhandled default case")
	}

	return nil
}*/

func parseDeliveredEvent(message string) (DeliveredEvent, error) {
	var event DeliveredEvent
	err := xml.Unmarshal([]byte(message), &event)
	return event, err
}

func parseEstablishedEvent(message string) (EstablishedEvent, error) {
	var event EstablishedEvent
	err := xml.Unmarshal([]byte(message), &event)
	return event, err
}

func (c *CSTAToolkit) processResponse(invokeID string, message string) error {
	c.callbackMu.RLock()
	_, exists := c.callbacks[invokeID]
	c.callbackMu.RUnlock()

	if !exists {
		return fmt.Errorf("no callback found for invokeID: %s", invokeID)
	}

	// Process response based on callback type
	// Implementation depends on specific response types needed

	c.callbackMu.Lock()
	delete(c.callbacks, invokeID)
	c.callbackMu.Unlock()

	return nil
}

// Add these structures after your existing type definitions
type Message struct {
	XMLName   xml.Name `xml:"message"`
	To        string   `xml:"to,attr"`
	From      string   `xml:"from,attr"`
	MsgID     string   `xml:"msgId,attr"`
	Name      string   `xml:"name,attr"`
	URL       string   `xml:"url,attr"`
	RecipType string   `xml:"recipType,attr"`
	Text      string   `xml:"text"`
	WebChat   *WebChat `xml:"webchat,omitempty"`
	ExtWeb    *ExtWeb  `xml:"extWeb,omitempty"`
}

type WebChat struct {
	RoomID        string `xml:"roomId"`
	CallGroupName string `xml:"callGroupName,omitempty"`
	Name          string `xml:"name,omitempty"`
	CID           string `xml:"cid,omitempty"`
	Page          string `xml:"page,omitempty"`
}

type ExtWeb struct {
	RoomID string `xml:"roomId"`
}

type MessageAck struct {
	XMLName   xml.Name `xml:"messageAck"`
	Code      string   `xml:"code,attr"`
	MsgID     string   `xml:"msgId,attr"`
	PersistID string   `xml:"persistId,attr"`
	RecipType string   `xml:"recipType,attr"`
	To        string   `xml:"to,attr"`
	ReqID     string   `xml:"reqId,attr"`
	From      string   `xml:"from,attr,omitempty"`
	Name      string   `xml:"name,attr,omitempty"`
	WebChat   *WebChat `xml:"webchat,omitempty"`
	ExtWeb    *ExtWeb  `xml:"extWeb,omitempty"`
	Typing    *Typing  `xml:"typing,omitempty"`
}

type Typing struct {
	ActionStart string `xml:"actionStart,attr"`
}

// Add these methods to your CSTAToolkit struct

func (c *CSTAToolkit) CreateWebChatMsgNew(message, roomID, name string) string {
	msg := Message{
		To:        "",
		MsgID:     "0",
		Name:      escapeXML(name),
		URL:       "",
		RecipType: "WebChat",
		Text:      escapeXML(message),
		WebChat: &WebChat{
			RoomID: roomID,
		},
	}

	data, err := xml.MarshalIndent(msg, "", "  ")
	if err != nil {
		return ""
	}

	xmlHeader := []byte(`<?xml version="1.0" encoding="UTF-8"?>`)
	data = append(xmlHeader, data...)

	return c.serializeHeader(len(data)) + string(data)
}

func (c *CSTAToolkit) CreateImMsgAckStatus(ackCode, msgID, persistID, recipID, groupID, recipType, from, fromName, roomID string, timestamp int64, action, groupName string) string {
	ack := MessageAck{
		Code:      ackCode,
		MsgID:     msgID,
		PersistID: persistID,
		RecipType: recipType,
		To:        "",
		ReqID:     "0",
		From:      from,
		Name:      escapeXML(fromName),
	}

	if ackCode == "IMS_TYPING" {
		ack.Typing = &Typing{
			ActionStart: action,
		}
		ack.WebChat = &WebChat{
			RoomID:        roomID,
			CallGroupName: groupName,
			Name:          fromName,
			CID:           "testChatCid",
			Page:          "testPage",
		}
	} else {
		if recipType == "WebChat" {
			ack.WebChat = &WebChat{
				RoomID: roomID,
			}
		} else if recipType == "ExtWeb" {
			ack.ExtWeb = &ExtWeb{
				RoomID: roomID,
			}
		}
	}

	data, err := xml.MarshalIndent(ack, "", "  ")
	if err != nil {
		return ""
	}

	xmlHeader := []byte(`<?xml version="1.0" encoding="UTF-8"?>`)
	data = append(xmlHeader, data...)

	return c.serializeHeader(len(data)) + string(data)
}

func (c *CSTAToolkit) CreateCommunication(ackCode, msgID, reqID, persistID, recipType, group, name string) string {
	ack := MessageAck{
		Code:      ackCode,
		MsgID:     msgID,
		PersistID: persistID,
		RecipType: recipType,
		To:        "",
		ReqID:     reqID,
		WebChat: &WebChat{
			CallGroupName: group,
			Name:          escapeXML(name),
			CID:           "testChatCid",
			Page:          "testPage",
		},
	}

	data, err := xml.MarshalIndent(ack, "", "  ")
	if err != nil {
		return ""
	}

	xmlHeader := []byte(`<?xml version="1.0" encoding="UTF-8"?>`)
	data = append(xmlHeader, data...)

	return c.serializeHeader(len(data)) + string(data)
}

// SetMessageHandlers adds message handlers to the toolkit
func (c *CSTAToolkit) SetMessageHandlers(msgHandler func(Message), ackHandler func(MessageAck)) {
	c.MessageHandler = msgHandler
	c.MessageAckHandler = ackHandler
}
