package main

import (
	"context"
	"flag"
	"log"
	"os/exec"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"

	"github.com/google/go-github/v76/github"
)

// Custom type to collect multiple flags into a slice
type stringSlice []string

func (s *stringSlice) String() string {
	return strings.Join(*s, ", ")
}

func (s *stringSlice) Set(value string) error {
	*s = append(*s, value)
	return nil
}

// If true the notification is discarded from the deletion pool
type FilterFunc func(*github.Notification) bool

func main() {
	var repos stringSlice
	var orgs stringSlice
	var dryRun bool

	flag.Var(&repos, "repo", "Specify a repository in the <owner>/<repository> format that should be skipped during deletion")
	flag.Var(&orgs, "org", "Specify a organization that should be skipped during deletion")
	flag.BoolVar(&dryRun, "dry-run", false, "Runs command and outputs notifications that would be deleted")

	flag.Parse()

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
	var filters []FilterFunc

	if len(repos) > 0 {
		filters = append(filters, createRepositoryFilter(repos))
	}

	if len(orgs) > 0 {
		filters = append(filters, createOrganizationFilter(orgs))
	}

	for i := 0; i < runtime.NumCPU(); i++ {
		wg.Add(1)
		go filter(allNotificationsChan, filteredNotificationsChan, ctx, client, filters, &wg)
	}

	for _, notification := range allNotifications {
		allNotificationsChan <- notification
	}

	close(allNotificationsChan)

	wg.Wait()

	for i := 0; i < runtime.NumCPU(); i++ {
		wg.Add(1)
		go remove(filteredNotificationsChan, ctx, client, dryRun, &wg)
	}

	close(filteredNotificationsChan)

	wg.Wait()
}

func filter(allNotifications chan *github.Notification, filteredNotifications chan *github.Notification, ctx context.Context, client *github.Client, filters []FilterFunc, wg *sync.WaitGroup) {
	defer wg.Done()

	for notification := range allNotifications {
		if notification.GetSubject().GetType() != "PullRequest" {
			continue
		}

		var skip bool
		for _, f := range filters {
			skip = skip || f(notification)
		}

		if skip {
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

func remove(removeNotifications chan *github.Notification, ctx context.Context, client *github.Client, dryRun bool, wg *sync.WaitGroup) {
	defer wg.Done()

	for notification := range removeNotifications {
		if !dryRun {
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
		}

		log.Printf("Removed notification for %s\n", notification.GetSubject().GetURL())
	}
}

func createRepositoryFilter(repos []string) FilterFunc {
	return func(notification *github.Notification) bool {
		for _, r := range repos {
			if notification.GetRepository().GetFullName() == r {
				return true
			}
		}
		return false
	}
}

func createOrganizationFilter(orgs []string) FilterFunc {
	return func(notification *github.Notification) bool {
		for _, o := range orgs {
			if notification.GetRepository().GetOwner().GetLogin() == o {
				return true
			}
		}
		return false
	}
}
