// Package client is the CLI's HTTP client for Deployable's REST API
// (/api/v1/...): package a directory, submit it, poll for completion, and
// fetch the resulting report.
package client

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"deployable/internal/analyzer"
	"deployable/internal/renderer"
)

// DefaultAPIURL is used when neither --api-url nor DEPLOYABLE_API_URL is set.
const DefaultAPIURL = "http://localhost:8080/api/v1"

const pollInterval = 2 * time.Second

// Client talks to Deployable's REST API.
type Client struct {
	baseURL string
	apiKey  string
	http    *http.Client
}

// New builds a Client. baseURL should not have a trailing slash.
func New(baseURL, apiKey string) *Client {
	return &Client{baseURL: baseURL, apiKey: apiKey, http: &http.Client{Timeout: 5 * time.Minute}}
}

// SubmitDirectory zips dir (excluding the same noise directories the server
// skips during analysis — no point uploading node_modules just to have it
// discarded) and uploads it, returning the created job ID.
func (c *Client) SubmitDirectory(ctx context.Context, dir string) (jobID string, err error) {
	zipData, err := zipDirectory(dir)
	if err != nil {
		return "", fmt.Errorf("package project: %w", err)
	}

	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	part, err := mw.CreateFormFile("file", "project.zip")
	if err != nil {
		return "", err
	}
	if _, err := part.Write(zipData); err != nil {
		return "", err
	}
	if err := mw.Close(); err != nil {
		return "", err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/analyze", &body)
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("X-API-Key", c.apiKey)

	resp, err := c.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("submit to deployable api: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusAccepted {
		return "", apiError(resp.StatusCode, respBody)
	}

	var created struct {
		JobID string `json:"job_id"`
	}
	if err := json.Unmarshal(respBody, &created); err != nil {
		return "", fmt.Errorf("decode response: %w", err)
	}
	return created.JobID, nil
}

// PollForResult polls job status until it completes or fails, then fetches
// and returns the full report.
func (c *Client) PollForResult(ctx context.Context, jobID string) (*renderer.Summary, string, error) {
	for {
		select {
		case <-ctx.Done():
			return nil, "", ctx.Err()
		case <-time.After(pollInterval):
		}

		status, err := c.jobStatus(ctx, jobID)
		if err != nil {
			return nil, "", err
		}

		switch status.Status {
		case "complete":
			if status.ReportSlug == "" {
				return nil, "", fmt.Errorf("job completed but no report was produced")
			}
			return c.fetchReport(ctx, status.ReportSlug)
		case "failed":
			msg := status.Message
			if msg == "" {
				msg = "analysis failed"
			}
			return nil, "", fmt.Errorf("%s", msg)
		}
	}
}

type jobStatusResponse struct {
	Status     string `json:"status"`
	Step       int    `json:"step"`
	TotalSteps int    `json:"total_steps"`
	Message    string `json:"message"`
	ReportSlug string `json:"report_slug"`
}

func (c *Client) jobStatus(ctx context.Context, jobID string) (*jobStatusResponse, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/analyze/"+jobID, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-API-Key", c.apiKey)

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("poll job status: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, apiError(resp.StatusCode, body)
	}

	var status jobStatusResponse
	if err := json.Unmarshal(body, &status); err != nil {
		return nil, fmt.Errorf("decode job status: %w", err)
	}
	return &status, nil
}

// reportResponse mirrors handlers.APIReportPayload — kept as a local copy so
// the CLI binary doesn't need to import the server's handlers package (and
// everything it pulls in: pgx, redis, the mailer, etc).
type reportResponse struct {
	URL             string   `json:"url"`
	Language        string   `json:"language"`
	LanguageVersion string   `json:"language_version"`
	Framework       string   `json:"framework"`
	Databases       []string `json:"databases"`

	ReadinessScore int `json:"readiness_score"`
	SecurityScore  int `json:"security_score"`

	ReadinessSummary string   `json:"readiness_summary"`
	CriticalGaps     []string `json:"critical_gaps"`
	Warnings         []string `json:"warnings"`
	Suggestions      []string `json:"suggestions"`

	MinRAMMB          *int     `json:"min_ram_mb"`
	RecRAMMB          *int     `json:"rec_ram_mb"`
	MinCPU            *float64 `json:"min_cpu"`
	StorageGB         *int     `json:"storage_gb"`
	EstRPS            *int     `json:"est_rps"`
	ResourceReasoning string   `json:"resource_reasoning"`

	Platforms []struct {
		Name       string `json:"name"`
		MonthlyUSD string `json:"monthly_usd"`
		Reasoning  string `json:"reasoning"`
	} `json:"platforms"`
}

func (c *Client) fetchReport(ctx context.Context, slug string) (*renderer.Summary, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/report/"+slug, nil)
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("X-API-Key", c.apiKey)

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("fetch report: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, "", apiError(resp.StatusCode, body)
	}

	var rr reportResponse
	if err := json.Unmarshal(body, &rr); err != nil {
		return nil, "", fmt.Errorf("decode report: %w", err)
	}

	s := &renderer.Summary{
		Language:          rr.Language,
		LanguageVersion:   rr.LanguageVersion,
		Framework:         rr.Framework,
		Databases:         rr.Databases,
		ReadinessScore:    rr.ReadinessScore,
		SecurityScore:     rr.SecurityScore,
		ReadinessSummary:  rr.ReadinessSummary,
		CriticalGaps:      rr.CriticalGaps,
		Warnings:          rr.Warnings,
		Suggestions:       rr.Suggestions,
		ResourceReasoning: rr.ResourceReasoning,
		ReportURL:         rr.URL,
	}
	if rr.MinRAMMB != nil {
		s.MinRAMMB = *rr.MinRAMMB
	}
	if rr.RecRAMMB != nil {
		s.RecRAMMB = *rr.RecRAMMB
	}
	if rr.MinCPU != nil {
		s.MinCPU = *rr.MinCPU
	}
	if rr.StorageGB != nil {
		s.StorageGB = *rr.StorageGB
	}
	if rr.EstRPS != nil {
		s.EstRPS = *rr.EstRPS
	}
	for _, p := range rr.Platforms {
		s.Platforms = append(s.Platforms, renderer.Platform{Name: p.Name, MonthlyUSD: p.MonthlyUSD, Reasoning: p.Reasoning})
	}

	return s, rr.URL, nil
}

func apiError(status int, body []byte) error {
	var e struct {
		Error string `json:"error"`
	}
	if json.Unmarshal(body, &e) == nil && e.Error != "" {
		return fmt.Errorf("deployable api: %s (HTTP %d)", e.Error, status)
	}
	return fmt.Errorf("deployable api returned HTTP %d", status)
}

// zipDirectory packages dir into an in-memory zip, skipping the same noise
// directories the server's analyzer excludes from its walk.
func zipDirectory(dir string) ([]byte, error) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)

	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if path == dir {
			return nil
		}

		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)

		if d.IsDir() {
			if analyzer.ShouldExcludeDir(d.Name()) {
				return filepath.SkipDir
			}
			return nil
		}

		info, err := d.Info()
		if err != nil {
			return nil // skip unreadable entry rather than aborting the whole zip
		}

		w, err := zw.CreateHeader(&zip.FileHeader{
			Name:     rel,
			Method:   zip.Deflate,
			Modified: info.ModTime(),
		})
		if err != nil {
			return err
		}

		f, err := os.Open(path)
		if err != nil {
			return nil
		}
		defer f.Close()

		_, err = io.Copy(w, f)
		return err
	})
	if err != nil {
		return nil, fmt.Errorf("walk directory: %w", err)
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
