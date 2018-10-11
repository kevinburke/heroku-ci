// The heroku-ci binary interacts with Heroku CI.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"time"

	"github.com/bgentry/go-netrc/netrc"
	git "github.com/kevinburke/go-git"
	types "github.com/kevinburke/go-types"
	"github.com/kevinburke/rest"
	"github.com/knq/ini"
)

const Version = "0.1"

type Client struct {
	*rest.Client
}

func (c *Client) NewRequest(method, path string, body io.Reader) (*http.Request, error) {
	req, err := c.Client.NewRequest(method, path, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.heroku+json; version=3")
	return req, nil
}

func getPipeline() string {
	// try to get the root
	root, err := git.Root("")
	if err != nil {
		return ""
	}
	f, err := os.Open(filepath.Join(root, ".git", "config"))
	if err != nil {
		return ""
	}
	file, err := ini.Load(f)
	if err != nil {
		return ""
	}
	section := file.GetSection("heroku")
	if section == nil {
		return ""
	}
	return section.Get("pipeline")
}

type Pipeline struct {
	CreatedAt time.Time        `json:"created_at"`
	ID        types.PrefixUUID `json:"id"`
	Name      string           `json:"name"`
	UpdatedAt time.Time        `json:"updated_at"`
}

type TestRun struct {
	CreatedAt     time.Time        `json:"created_at"`
	ID            types.PrefixUUID `json:"id"`
	UpdatedAt     time.Time        `json:"updated_at"`
	ClearCache    bool             `json:"clear_cache"`
	CommitBranch  string           `json:"commit_branch"`
	CommitSHA     string           `json:"commit_sha"`
	CommitMessage string           `json:"commit_message"`
	Status        string           `json:"status"`
}

func (t TestRun) InProgress() bool {
	return t.Status != "succeeded" && t.Status != "failed" && t.Status != "errored"
}

// Given a set of command line args, return the git branch or an error. Returns
// the current git branch if no argument is specified
func getBranchFromArgs(args []string) (string, error) {
	if len(args) == 0 {
		return git.CurrentBranch()
	} else {
		return args[0], nil
	}
}

// getMinTipLength compares two strings and returns the length of the
// shortest
func getMinTipLength(remoteTip string, localTip string) int {
	if len(remoteTip) <= len(localTip) {
		return len(remoteTip)
	}
	return len(localTip)
}

func getTestRuns(client *Client, id types.PrefixUUID, args []string) error {
	branch, err := getBranchFromArgs(args)
	if err != nil {
		return err
	}
	remote, err := git.GetRemoteURL("origin")
	if err != nil {
		return err
	}
	_ = remote
	tip, err := git.Tip(branch)
	if err != nil {
		return err
	}
	req, err := client.NewRequest("GET", "/pipelines/"+id.String()+"/test-runs", nil)
	if err != nil {
		return err
	}
	runs := make([]*TestRun, 0)
	if err := client.Do(req, &runs); err != nil {
		return err
	}
	var foundRun *TestRun
	for i := range runs {
		if runs[i].CommitBranch != branch {
			continue
		}
		maxTipLengthToCompare := getMinTipLength(runs[i].CommitSHA, tip)
		if runs[i].CommitSHA[:maxTipLengthToCompare] == tip[:maxTipLengthToCompare] {
			foundRun = runs[i]
			break
		}
	}
	if foundRun == nil {
		return fmt.Errorf("Could not find test run for commit %s\n", tip[:8])
	}
	count := 0
	for foundRun.InProgress() {
		dur := time.Since(foundRun.CreatedAt)
		if dur > time.Minute {
			dur = dur.Round(time.Second)
		} else {
			dur = dur.Round(10 * time.Millisecond)
		}
		if count%5 == 0 {
			fmt.Printf("status is %q, running for %s, sleeping...\n", foundRun.Status, dur)
		}
		count++
		time.Sleep(2 * time.Second)
		req, err := client.NewRequest("GET", "/test-runs/"+foundRun.ID.String(), nil)
		if err != nil {
			return err
		}
		if err := client.Do(req, &foundRun); err != nil {
			return err
		}
	}
	fmt.Printf("Test run %q completed with status %s! Exiting.\n", foundRun.ID.String()[:8], foundRun.Status)
	return nil
}

const help = `The heroku-ci binary interacts with Heroku CI.

Usage: 

	heroku-ci command [arguments]

The commands are:

	version             Print the current version
	wait                Wait for tests to finish on a branch.

Use "travis help [command]" for more information about a command.
`

func usage() {
	fmt.Fprintf(os.Stderr, help)
	flag.PrintDefaults()
}

func main() {
	flag.Parse()
	args := flag.Args()
	if len(args) < 1 {
		usage()
		os.Exit(2)
	}
	ctx, cancel := context.WithCancel(context.Background())
	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt)
	go func() {
		<-c
		cancel()
	}()
	subargs := args[1:]
	switch flag.Arg(0) {
	case "wait":
		homedir := os.UserHomeDir()
		machine, err := netrc.FindMachine(filepath.Join(homedir, ".netrc"), "api.heroku.com")
		if err != nil {
			log.Fatal(err)
		}
		client := &Client{
			rest.NewClient(machine.Login, machine.Password, "https://api.heroku.com"),
		}
		client.Client.Client.Timeout = 0
		pipelineName := getPipeline()
		req, err := client.NewRequest("GET", "/pipelines", nil)
		if err != nil {
			log.Fatal(err)
		}
		req = req.WithContext(ctx)
		pipelineBody := make([]*Pipeline, 0)
		if err := client.Do(req, &pipelineBody); err != nil {
			log.Fatal(err)
		}
		var ourPipeline *Pipeline
		for i := range pipelineBody {
			if pipelineBody[i].Name == pipelineName {
				ourPipeline = pipelineBody[i]
				break
			}
		}
		if ourPipeline == nil {
			log.Fatalf("could not find pipeline named %q", pipelineName)
		}
		if err := getTestRuns(client, ourPipeline.ID, subargs); err != nil {
			log.Fatal(err)
		}
	case "version":
		fmt.Fprintf(os.Stderr, "heroku-ci version %s\n", Version)
		os.Exit(1)
	default:
		fmt.Fprintf(os.Stderr, "heroku-ci: unknown command %q\n\n", flag.Arg(0))
		usage()
		os.Exit(2)
	}
}
