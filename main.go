package main

import (
	"encoding/json"
	"fmt"
	"log"
	"os/exec"
	"runtime"
	"strings"
	"sync"
)

type Notification struct {
	ID      string `json:"id"`
	Subject struct {
		URL  string `json:"url"`
		Type string `json:"type"`
	} `json:"subject"`
}

func main() {
	notificationsCommand := exec.Command("gh", "api", "/notifications", "--paginate")

	notificationsOutput, err := notificationsCommand.Output()
	if err != nil {
		log.Fatal(err)
	}

	// fmt.Println(string(notificationsOutput))

	var rawNotifications []Notification

	err = json.Unmarshal(notificationsOutput, &rawNotifications)
	if err != nil {
		log.Fatal(err)
	}

	rawNotificationsChan := make(chan Notification, len(rawNotifications))
	filteredNotificationsChan := make(chan Notification, len(rawNotifications))

	var wg sync.WaitGroup

	for i := 0; i < runtime.NumCPU(); i++ {
		wg.Add(1)
		go filter(rawNotificationsChan, filteredNotificationsChan, &wg)
	}

	for _, notification := range rawNotifications {
		rawNotificationsChan <- notification
	}

	close(rawNotificationsChan)

	wg.Wait()

	for i := 0; i < runtime.NumCPU(); i++ {
		wg.Add(1)
		go remove(filteredNotificationsChan, &wg)
	}

	close(filteredNotificationsChan)

	wg.Wait()
}

func filter(rawNotifications chan Notification, filteredNotifications chan Notification, wg *sync.WaitGroup) {
	defer wg.Done()

	for notification := range rawNotifications {
		if notification.Subject.Type != "PullRequest" {
			continue
		}

		apiEndpoint := strings.TrimPrefix(notification.Subject.URL, "https://api.github.com")
		PRStateCheckCommand := exec.Command("gh", "api", apiEndpoint)
		PRStateCheckOuput, err := PRStateCheckCommand.Output()
		if err != nil {
			log.Println(err)
			continue
		}

		var PRState struct {
			State string `json:"state"`
		}

		json.Unmarshal(PRStateCheckOuput, &PRState)

		if PRState.State == "closed" {
			filteredNotifications <- notification
		}
	}
}

func remove(removeNotifications chan Notification, wg *sync.WaitGroup) {
	defer wg.Done()

	for notification := range removeNotifications {
		apiEndpoint := fmt.Sprintf("/notifications/threads/%s", notification.ID)
		RemoveNotificationCommand := exec.Command("gh", "api", "--method", "DELETE", apiEndpoint)
		err := RemoveNotificationCommand.Run()
		if err != nil {
			log.Println(err)
			continue
		}
		log.Printf("Removed notification for %s\n", notification.Subject.URL)
	}
}
