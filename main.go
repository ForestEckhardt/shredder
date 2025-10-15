package main

import (
	"context"
	"log"
	"os/exec"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"

	"github.com/google/go-github/v76/github"
)

func main() {
	token, err := exec.Command("gh", "auth", "token").Output()
	if err != nil {
		log.Fatal(err)
	}

	ctx := context.Background()

	client := github.NewClient(nil).WithAuthToken(strings.TrimSpace(string(token)))

	// Get all notifications
	opts := &github.NotificationListOptions{ListOptions: github.ListOptions{PerPage: 100}}

	var allNotifications []*github.Notification
	for {
		notifications, resp, err := client.Activity.ListNotifications(ctx, opts)
		if err != nil {
			log.Fatalf("failed to list notifications: %v", err)
		}

		allNotifications = append(allNotifications, notifications...)
		if resp.NextPage == 0 {
			break
		}
		opts.Page = resp.NextPage
	}

	allNotificationsChan := make(chan *github.Notification, len(allNotifications))
	filteredNotificationsChan := make(chan *github.Notification, len(allNotifications))

	var wg sync.WaitGroup

	for i := 0; i < runtime.NumCPU(); i++ {
		wg.Add(1)
		go filter(allNotificationsChan, filteredNotificationsChan, ctx, client, &wg)
	}

	for _, notification := range allNotifications {
		allNotificationsChan <- notification
	}

	close(allNotificationsChan)

	wg.Wait()

	for i := 0; i < runtime.NumCPU(); i++ {
		wg.Add(1)
		go remove(filteredNotificationsChan, ctx, client, &wg)
	}

	close(filteredNotificationsChan)

	wg.Wait()
}

func filter(allNotifications chan *github.Notification, filteredNotifications chan *github.Notification, ctx context.Context, client *github.Client, wg *sync.WaitGroup) {
	defer wg.Done()

	for notification := range allNotifications {
		if notification.GetSubject().GetType() != "PullRequest" {
			continue
		}

		apiURL := notification.GetSubject().GetURL()

		re := regexp.MustCompile(`https:\/\/api\.github\.com\/repos\/([\w|-]+)\/([\w|-]+)\/pulls\/(\d+)`)
		matches := re.FindStringSubmatch(apiURL)

		owner := matches[1]
		repo := matches[2]
		prNumber, err := strconv.Atoi(matches[3])
		if err != nil {
			log.Println(err)
			continue
		}

		pr, _, err := client.PullRequests.Get(ctx, owner, repo, prNumber)
		if err != nil {
			log.Printf("failed to fetch PR for %s: %v", apiURL, err)
			continue
		}

		if pr.GetState() == "closed" {
			filteredNotifications <- notification
		}
	}
}

func remove(removeNotifications chan *github.Notification, ctx context.Context, client *github.Client, wg *sync.WaitGroup) {
	defer wg.Done()

	for notification := range removeNotifications {
		notificationID, err := strconv.ParseInt(notification.GetID(), 0, 64)
		if err != nil {
			log.Println(err)
			continue
		}

		_, err = client.Activity.MarkThreadDone(ctx, notificationID)
		if err != nil {
			log.Printf("failed to mark notification %s as done %v", notification.GetID(), err)
			continue
		}

		log.Printf("Removed notification for %s\n", notification.GetSubject().GetURL())
	}
}
