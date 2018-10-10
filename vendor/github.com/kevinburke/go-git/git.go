package git

import (
	"errors"
	"fmt"
	"log"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

const version = "0.6"

type GitFormat int

const (
	SSHFormat   GitFormat = iota
	HTTPSFormat           = iota
)

// Short ssh style doesn't allow a custom port
// http://stackoverflow.com/a/5738592/329700
var sshExp = regexp.MustCompile(`^(?P<sshUser>[^@]+)@(?P<domain>[^:]+):(?P<pathRepo>.*)(\.git/?)?$`)

// https://github.com/kevinburke/go-circle.git
var httpsExp = regexp.MustCompile(`^https://(?P<domain>[^/:]+)(:(?P<port>[[0-9]+))?/(?P<pathRepo>.+?)(\.git/?)?$`)

// A remote URL. Easiest to describe with an example:
//
// git@github.com:kevinburke/go-circle.git
//
// Would be parsed as follows:
//
// Path     = kevinburke
// Host     = github.com
// RepoName = go-circle
// SSHUser  = git
// URL      = git@github.com:kevinburke/go-circle.git
// Format   = SSHFormat
//
// Similarly:
//
// https://github.com/kevinburke/go-circle.git
//
// User     = kevinburke
// Host     = github.com
// RepoName = go-circle
// SSHUser  = ""
// Format   = HTTPSFormat
type RemoteURL struct {
	Host     string
	Port     int
	Path     string
	RepoName string
	Format   GitFormat

	// The full URL
	URL string

	// If the remote uses the SSH format, this is the name of the SSH user for
	// the remote. Usually "git@"
	SSHUser string
}

func getPathAndRepoName(pathAndRepo string) (string, string) {
	if strings.HasSuffix(pathAndRepo, "/") {
		pathAndRepo = pathAndRepo[:len(pathAndRepo)-1]
	}
	paths := strings.Split(pathAndRepo, "/")
	repoName := paths[len(paths)-1]
	path := strings.Join(paths[:len(paths)-1], "/")
	// there is probably a way to put this in the regex.
	if strings.HasSuffix(repoName, ".git") {
		repoName = repoName[:len(repoName)-len(".git")]
	}
	return path, repoName
}

// ParseRemoteURL takes a git remote URL and returns an object with its
// component parts, or an error if the remote cannot be parsed
func ParseRemoteURL(remoteURL string) (*RemoteURL, error) {
	remoteURL = strings.TrimSpace(remoteURL)
	match := sshExp.FindStringSubmatch(remoteURL)
	if len(match) > 0 {
		path, repoName := getPathAndRepoName(match[3])
		return &RemoteURL{
			Path:     path,
			Host:     match[2],
			RepoName: repoName,
			URL:      match[0],
			Port:     22,

			Format:  SSHFormat,
			SSHUser: match[1],
		}, nil
	}
	match = httpsExp.FindStringSubmatch(remoteURL)
	if len(match) > 0 {
		var port int
		var err error
		if len(match[3]) > 0 {
			port, err = strconv.Atoi(match[3])
			if err != nil {
				log.Panicf("git: invalid port: %s", match[3])
			}
		} else {
			port = 443
		}
		path, repoName := getPathAndRepoName(match[4])
		return &RemoteURL{
			Path:     path,
			Host:     match[1],
			RepoName: repoName,
			URL:      match[0],
			Port:     port,

			Format: HTTPSFormat,
		}, nil
	}
	return nil, fmt.Errorf("Could not parse %s as a git remote", remoteURL)
}

// RemoteURL returns a Remote object with information about the given Git
// remote.
func GetRemoteURL(remoteName string) (*RemoteURL, error) {
	rawRemote, err := exec.Command("git", "config", "--get", fmt.Sprintf("remote.%s.url", remoteName)).Output()
	if err != nil {
		return nil, err
	}
	// git response includes a newline
	remote := strings.TrimSpace(string(rawRemote))
	return ParseRemoteURL(remote)
}

// CurrentBranch returns the name of the current Git branch. Returns an error
// if you are not on a branch, or if you are not in a git repository.
func CurrentBranch() (string, error) {
	result, err := exec.Command("git", "symbolic-ref", "--short", "HEAD").Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(result)), nil
}

// Tip returns the SHA of the given Git branch. If the empty string is
// provided, defaults to HEAD on the current branch.
func Tip(branch string) (string, error) {
	if branch == "" {
		branch = "HEAD"
	}
	result, err := exec.Command("git", "rev-parse", "--short", branch).CombinedOutput()
	if err != nil {
		if strings.Contains(string(result), "Needed a single revision") {
			return "", fmt.Errorf("git: Branch %s is unknown, can't get tip", branch)
		}
		return "", err
	}
	return strings.TrimSpace(string(result)), nil
}

// Root returns the root directory of the current Git repository, or an error
// if you are not in a git repository. If directory is not the empty string,
// change the working directory before running the command.
func Root(directory string) (string, error) {
	cmd := exec.Command("git", "rev-parse", "--show-toplevel")
	cmd.Dir = filepath.Dir(directory)
	result, err := cmd.CombinedOutput()
	trimmed := strings.TrimSpace(string(result))
	if err != nil {
		return "", errors.New(trimmed)
	}
	return strings.TrimSpace(trimmed), nil
}
