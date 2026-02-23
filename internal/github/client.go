package github

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Client GitHub API 客户端
type Client struct {
	token      string
	owner      string
	repo       string
	httpClient *http.Client
}

func NewClient(token, owner, repo string) *Client {
	return &Client{
		token: token,
		owner: owner,
		repo:  repo,
		httpClient: &http.Client{
			Timeout: 15 * time.Second,
		},
	}
}

type CreateIssueInput struct {
	Title  string   `json:"title"`
	Body   string   `json:"body"`
	Labels []string `json:"labels"`
}

type CreateIssueResult struct {
	Number int
	URL    string
}

type IssueStatus struct {
	Number    int
	Title     string
	State     string
	Labels    []string
	UpdatedAt time.Time
	URL       string
	Comments  []IssueComment
}

type IssueComment struct {
	ID        int64
	Author    string
	Body      string
	CreatedAt time.Time
}

func (c *Client) CreateIssue(ctx context.Context, input CreateIssueInput) (CreateIssueResult, error) {
	payload, err := json.Marshal(input)
	if err != nil {
		return CreateIssueResult{}, fmt.Errorf("编码 issue 请求失败: %w", err)
	}

	endpoint := fmt.Sprintf("https://api.github.com/repos/%s/%s/issues", c.owner, c.repo)
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return CreateIssueResult{}, fmt.Errorf("创建 issue 请求失败: %w", err)
	}

	c.fillHeaders(request)

	response, err := c.httpClient.Do(request)
	if err != nil {
		return CreateIssueResult{}, fmt.Errorf("调用 GitHub 创建 issue 失败: %w", err)
	}
	defer response.Body.Close()

	body, _ := io.ReadAll(response.Body)
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return CreateIssueResult{}, fmt.Errorf("GitHub 创建 issue 失败: HTTP %d, body=%s", response.StatusCode, string(body))
	}

	var result struct {
		Number  int    `json:"number"`
		HTMLURL string `json:"html_url"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return CreateIssueResult{}, fmt.Errorf("解析 GitHub 创建 issue 响应失败: %w", err)
	}

	return CreateIssueResult{Number: result.Number, URL: result.HTMLURL}, nil
}

func (c *Client) GetIssueStatus(ctx context.Context, issueNumber int) (IssueStatus, error) {
	endpoint := fmt.Sprintf("https://api.github.com/repos/%s/%s/issues/%d", c.owner, c.repo, issueNumber)
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return IssueStatus{}, fmt.Errorf("创建 issue 查询请求失败: %w", err)
	}

	c.fillHeaders(request)

	response, err := c.httpClient.Do(request)
	if err != nil {
		return IssueStatus{}, fmt.Errorf("调用 GitHub 查询 issue 失败: %w", err)
	}
	defer response.Body.Close()

	data, _ := io.ReadAll(response.Body)
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return IssueStatus{}, fmt.Errorf("GitHub 查询 issue 失败: HTTP %d, body=%s", response.StatusCode, string(data))
	}

	var issue struct {
		Number    int    `json:"number"`
		Title     string `json:"title"`
		State     string `json:"state"`
		UpdatedAt string `json:"updated_at"`
		HTMLURL   string `json:"html_url"`
		Labels    []struct {
			Name string `json:"name"`
		} `json:"labels"`
		CommentsURL string `json:"comments_url"`
	}

	if err := json.Unmarshal(data, &issue); err != nil {
		return IssueStatus{}, fmt.Errorf("解析 issue 响应失败: %w", err)
	}

	updatedAt, err := time.Parse(time.RFC3339, issue.UpdatedAt)
	if err != nil {
		updatedAt = time.Now()
	}

	labels := make([]string, 0, len(issue.Labels))
	for _, label := range issue.Labels {
		if name := strings.TrimSpace(label.Name); name != "" {
			labels = append(labels, name)
		}
	}

	comments, err := c.fetchComments(ctx, issue.CommentsURL)
	if err != nil {
		return IssueStatus{}, err
	}

	return IssueStatus{
		Number:    issue.Number,
		Title:     issue.Title,
		State:     issue.State,
		Labels:    labels,
		UpdatedAt: updatedAt,
		URL:       issue.HTMLURL,
		Comments:  comments,
	}, nil
}

func (c *Client) fetchComments(ctx context.Context, endpoint string) ([]IssueComment, error) {
	if strings.TrimSpace(endpoint) == "" {
		return []IssueComment{}, nil
	}

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("创建 comments 请求失败: %w", err)
	}

	c.fillHeaders(request)

	response, err := c.httpClient.Do(request)
	if err != nil {
		return nil, fmt.Errorf("调用 GitHub comments 失败: %w", err)
	}
	defer response.Body.Close()

	data, _ := io.ReadAll(response.Body)
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("GitHub comments 失败: HTTP %d, body=%s", response.StatusCode, string(data))
	}

	var raw []struct {
		ID        int64  `json:"id"`
		Body      string `json:"body"`
		CreatedAt string `json:"created_at"`
		User      struct {
			Login string `json:"login"`
		} `json:"user"`
	}

	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("解析 comments 响应失败: %w", err)
	}

	comments := make([]IssueComment, 0, len(raw))
	for _, item := range raw {
		createdAt, err := time.Parse(time.RFC3339, item.CreatedAt)
		if err != nil {
			createdAt = time.Now()
		}
		comments = append(comments, IssueComment{
			ID:        item.ID,
			Author:    item.User.Login,
			Body:      item.Body,
			CreatedAt: createdAt,
		})
	}

	return comments, nil
}

func (c *Client) fillHeaders(request *http.Request) {
	request.Header.Set("Authorization", fmt.Sprintf("Bearer %s", c.token))
	request.Header.Set("Accept", "application/vnd.github+json")
	request.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	request.Header.Set("User-Agent", "ELS-Feedback-Proxy")
}
