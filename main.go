package main

import (
	"encoding/json"
	"fmt"
	"log"
	csta "zultys-csta-go/csta"
)

func main() {
	client := csta.NewCSTAClient("", "")

	// Connect to the server
	if err := client.Connect(); err != nil {
		log.Fatal(err)
	}
	defer client.Close()

	// Add custom handler
	client.RegisterHandler("holidaySettings", func(data json.RawMessage) error {
		println(data)
		return nil
	})
	client.RegisterHandler("activityClient", func(data json.RawMessage) error {
		var activityResponse struct {
			ActivityClientList []struct {
				ActivityClient struct {
					AgentVersion string `json:"agentVersion"`
					Platform     string `json:"platform"`
					// Include other necessary fields
				} `json:"activityClient"`
			} `json:"activityClientList"`
			Total int `json:"total"`
		}
		if err := json.Unmarshal(data, &activityResponse); err != nil {
			return fmt.Errorf("failed to parse activityClient data: %v", err)
		}
		// Process activityResponse here
		log.Printf("Received %d activity clients", activityResponse.Total)
		return nil
	})

	// Subscribe to various updates
	if err := client.SubscribeToAdmin(""); err != nil {
		log.Printf("Subscribed: %v", err)
	}
	if err := client.SubscribeToAdmin("activityClient"); err != nil {
		log.Printf("Subscribed: %v", err)
	}
	if err := client.SubscribeToAdmin("holidaySettings"); err != nil {
		log.Printf("holidaySettings: %v", err)
	}

	// Keep the program running
	select {}
}

/*func main() {
	config := Config{
		ServerAddress: "",
		GroupName:     "",
		CustomName:    "",
	}

	for i := 0; i < 10; i++ {
		config.CustomName += strconv.Itoa(i)
		client := NewChatClient(config)

		// Set up callbacks
		client.OnMessage = func(msg ChatMessage) {
			fmt.Printf("ChatMessage from %s: %s\n", msg.FromName, msg.Text)
		}

		client.OnAgentJoined = func(agent string) {
			fmt.Printf("Agent %s joined the chat\n", agent)
		}

		// Connect to the chat server
		err := client.Connect(config)
		if err != nil {
			log.Fatalf("Failed to connect: %v", err)
		}

		// Send a message
		err = client.SendMessage("Hello!")
		if err != nil {
			log.Printf("Failed to send message: %v", err)
		}

		// Indicate typing
		client.StartTyping()
		time.Sleep(2 * time.Second)
		client.StopTyping()

		// Close the connection when done
		defer client.Close()
	}
	select {}
}*/
