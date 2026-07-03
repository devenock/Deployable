// Package github is a minimal GitHub REST API client: enough to resolve a
// pasted repository URL to owner/repo, check whether it's accessible, and
// download its default-branch source as a zip archive. It intentionally
// doesn't wrap the full GitHub API surface — Deployable only ever reads a
// repo once per analysis.
package github

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"strings"
	"time"
)

const apiBaseURL = "https://api.github.com"

// ErrNotFound covers both "repository does not exist" and "exists but not
// accessible with the current credentials" — GitHub's API deliberately
// returns 404 for both, to avoid leaking the existence of private repos.
var ErrNotFound = errors.New("repository not found or not accessible")

// ErrRateLimited means the request was rejected by GitHub's rate limiter
// (60/hour unauthenticated, 5000/hour authenticated).
var ErrRateLimited = errors.New("github api rate limit exceeded")

// Client is a small GitHub REST API client. An empty token means
// unauthenticated requests — public repositories only, 60 requests/hour.
type Client struct {
	token string
	http  *http.Client
}

// NewClient builds a Client. Pass an empty token for unauthenticated access.
func NewClient(token string) *Client {
	return &Client{token: token, http: &http.Client{Timeout: 60 * time.Second}}
}

// RepoInfo is the subset of GitHub's repository metadata Deployable needs.
type RepoInfo struct {
	FullName      string
	DefaultBranch string
	Private       bool
}

// repoURLPattern accepts "github.com/owner/repo" with or without a scheme,
// a trailing ".git", or a trailing slash.
var repoURLPattern = regexp.MustCompile(`^(?:https?://)?(?:www\.)?github\.com/([\w.-]+)/([\w.-]+?)(?:\.git)?/?$`)

// ParseRepoURL extracts owner/repo from a pasted GitHub URL.
func ParseRepoURL(raw string) (owner, repo string, err error) {
	raw = strings.TrimSpace(raw)
	m := repoURLPattern.FindStringSubmatch(raw)
	if m == nil {
		return "", "", fmt.Errorf("not a valid github.com/owner/repo URL")
	}
	return m[1], m[2], nil
}

func (c *Client) newRequest(ctx context.Context, method, path string) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, method, apiBaseURL+path, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	return req, nil
}

// GetRepo fetches repository metadata.
func (c *Client) GetRepo(ctx context.Context, owner, repo string) (*RepoInfo, error) {
	req, err := c.newRequest(ctx, http.MethodGet, "/repos/"+owner+"/"+repo)
	if err != nil {
		return nil, err
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("github api request: %w", err)
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusNotFound:
		return nil, ErrNotFound
	case http.StatusForbidden:
		return nil, ErrRateLimited
	default:
		return nil, fmt.Errorf("github api returned %d", resp.StatusCode)
	}

	var body struct {
		FullName      string `json:"full_name"`
		DefaultBranch string `json:"default_branch"`
		Private       bool   `json:"private"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, fmt.Errorf("decode repo response: %w", err)
	}

	return &RepoInfo{FullName: body.FullName, DefaultBranch: body.DefaultBranch, Private: body.Private}, nil
}

// DownloadZipball streams the repo's zipball archive for ref to destPath.
// The read is capped at maxBytes+1: if the returned written count exceeds
// maxBytes, the caller should treat it as "too large" and discard the file.
func (c *Client) DownloadZipball(ctx context.Context, owner, repo, ref, destPath string, maxBytes int64) (written int64, err error) {
	req, err := c.newRequest(ctx, http.MethodGet, "/repos/"+owner+"/"+repo+"/zipball/"+ref)
	if err != nil {
		return 0, err
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return 0, fmt.Errorf("download zipball: %w", err)
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusNotFound:
		return 0, ErrNotFound
	case http.StatusForbidden:
		return 0, ErrRateLimited
	default:
		return 0, fmt.Errorf("github zipball download returned %d", resp.StatusCode)
	}

	out, err := os.Create(destPath)
	if err != nil {
		return 0, err
	}
	defer out.Close()

	n, err := io.Copy(out, io.LimitReader(resp.Body, maxBytes+1))
	if err != nil {
		return n, fmt.Errorf("write zipball: %w", err)
	}
	return n, nil
}
