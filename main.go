package main

import (
	"encoding/json"
	"fmt"
	"log"
	"time"
	"zultys-csta-go/admin_csta"
)

func main() {
	client := admin_csta.NewCSTAClient("", "")

	// Connect to the server
	if err := client.Connect(); err != nil {
		log.Fatal(err)
	}
	defer client.Close()

	client.RegisterHandler("activityClient", func(data json.RawMessage) error {
		var activityResponse struct {
			ActivityClientList []struct {
				ActivityClient struct {
					AccountId      string `json:"accountId"`
					AccountName    string `json:"accountName"`
					AgentName      string `json:"agentName"`
					AgentVersion   string `json:"agentVersion"`
					ClientType     string `json:"clientType"`
					Extension      string `json:"extension"`
					GmtConnectTime int    `json:"gmtConnectTime"`
					Ip             struct {
						Addr string `json:"addr"`
						Port int    `json:"port"`
					} `json:"ip"`
					LocationId string `json:"locationId"`
					Platform   string `json:"platform"`
					Status     string `json:"status"`
					Tid        string `json:"tid"`
				} `json:"activityClient"`
			} `json:"activityClientList"`
			Total int `json:"total"`
		}
		if err := json.Unmarshal(data, &activityResponse); err != nil {
			return fmt.Errorf("failed to parse activityClient data: %v", err)
		}
		// Process activityResponse here
		log.Printf("Received %d activity clients", activityResponse.Total)

		marshal, err := json.Marshal(activityResponse.ActivityClientList)
		if err != nil {
			return err
		}

		log.Printf("Activity Clients: %s", marshal)
		return nil
	})

	// Subscribe to various updates
	if err := client.SubscribeToAdmin("activityClient"); err != nil {
		log.Printf("Subscribed: %v", err)
	}

	time.Sleep(2 * time.Second)
}

/*func main() {
	config := chat_csta.Config{
		ServerAddress: "108.165.150.61",
		GroupName:     "",
		CustomName:    "",
	}

	client := chat_csta.NewChatClient(config)
	client.LoginZAC(config)

	select {}
}
*/
