package github

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
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

type CreateCommentResult struct {
	ID        int64
	Author    string
	Body      string
	CreatedAt time.Time
	URL       string
}

type IssueStatus struct {
	Number    int
	Title     string
	Body      string
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

func (c *Client) CreateIssueComment(ctx context.Context, issueNumber int, body string) (CreateCommentResult, error) {
	requestBody, err := json.Marshal(map[string]string{
		"body": body,
	})
	if err != nil {
		return CreateCommentResult{}, fmt.Errorf("编码 comment 请求失败: %w", err)
	}

	endpoint := fmt.Sprintf("https://api.github.com/repos/%s/%s/issues/%d/comments", c.owner, c.repo, issueNumber)
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(requestBody))
	if err != nil {
		return CreateCommentResult{}, fmt.Errorf("创建 comment 请求失败: %w", err)
	}

	c.fillHeaders(request)
	response, err := c.httpClient.Do(request)
	if err != nil {
		return CreateCommentResult{}, fmt.Errorf("调用 GitHub comment 失败: %w", err)
	}
	defer response.Body.Close()

	data, _ := io.ReadAll(response.Body)
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return CreateCommentResult{}, fmt.Errorf("GitHub 创建 comment 失败: HTTP %d, body=%s", response.StatusCode, string(data))
	}

	var payload struct {
		ID        int64  `json:"id"`
		Body      string `json:"body"`
		CreatedAt string `json:"created_at"`
		HTMLURL   string `json:"html_url"`
		User      struct {
			Login string `json:"login"`
		} `json:"user"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return CreateCommentResult{}, fmt.Errorf("解析 comment 响应失败: %w", err)
	}

	createdAt, err := time.Parse(time.RFC3339, payload.CreatedAt)
	if err != nil {
		createdAt = time.Now()
	}

	return CreateCommentResult{
		ID:        payload.ID,
		Author:    strings.TrimSpace(payload.User.Login),
		Body:      payload.Body,
		CreatedAt: createdAt,
		URL:       payload.HTMLURL,
	}, nil
}

func (c *Client) GetAuthenticatedLogin(ctx context.Context) (string, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.github.com/user", nil)
	if err != nil {
		return "", fmt.Errorf("创建 /user 请求失败: %w", err)
	}
	c.fillHeaders(request)

	response, err := c.httpClient.Do(request)
	if err != nil {
		return "", fmt.Errorf("调用 GitHub /user 失败: %w", err)
	}
	defer response.Body.Close()

	data, _ := io.ReadAll(response.Body)
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return "", fmt.Errorf("GitHub /user 失败: HTTP %d, body=%s", response.StatusCode, string(data))
	}

	var payload struct {
		Login string `json:"login"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return "", fmt.Errorf("解析 /user 响应失败: %w", err)
	}
	login := strings.TrimSpace(payload.Login)
	if login == "" {
		return "", fmt.Errorf("GitHub /user 未返回 login")
	}
	return login, nil
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
		Body      string `json:"body"`
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
		Body:      issue.Body,
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

	comments := make([]IssueComment, 0, 32)
	for page := 1; page <= 10; page++ {
		requestURL, err := appendPagination(endpoint, page, 100)
		if err != nil {
			return nil, err
		}

		request, err := http.NewRequestWithContext(ctx, http.MethodGet, requestURL, nil)
		if err != nil {
			return nil, fmt.Errorf("创建 comments 请求失败: %w", err)
		}
		c.fillHeaders(request)

		response, err := c.httpClient.Do(request)
		if err != nil {
			return nil, fmt.Errorf("调用 GitHub comments 失败: %w", err)
		}

		data, _ := io.ReadAll(response.Body)
		response.Body.Close()
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

		for _, item := range raw {
			createdAt, err := time.Parse(time.RFC3339, item.CreatedAt)
			if err != nil {
				createdAt = time.Now()
			}
			comments = append(comments, IssueComment{
				ID:        item.ID,
				Author:    strings.TrimSpace(item.User.Login),
				Body:      item.Body,
				CreatedAt: createdAt,
			})
		}

		if len(raw) < 100 {
			break
		}
	}

	return comments, nil
}

func appendPagination(endpoint string, page int, perPage int) (string, error) {
	parsed, err := url.Parse(endpoint)
	if err != nil {
		return "", fmt.Errorf("comments URL 无效: %w", err)
	}
	query := parsed.Query()
	query.Set("page", fmt.Sprintf("%d", page))
	query.Set("per_page", fmt.Sprintf("%d", perPage))
	parsed.RawQuery = query.Encode()
	return parsed.String(), nil
}

func (c *Client) fillHeaders(request *http.Request) {
	request.Header.Set("Authorization", fmt.Sprintf("Bearer %s", c.token))
	request.Header.Set("Accept", "application/vnd.github+json")
	request.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	request.Header.Set("User-Agent", "ELS-Feedback-Proxy")
}
