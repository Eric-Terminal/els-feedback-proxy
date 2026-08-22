package github

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"strings"
)

const (
	maxSourceTreeDirectories = 10_000
	maxSourceTreeEntries     = 100_000
)

type SourceTreeEntry struct {
	Path string `json:"path"`
	Type string `json:"type"`
	Size *int   `json:"size,omitempty"`
	SHA  string `json:"sha,omitempty"`
}

type SourceTree struct {
	Entries   []SourceTreeEntry `json:"entries"`
	Truncated bool              `json:"truncated"`
}

// GetSourceTree 优先使用 GitHub 递归树；命中截断上限时逐目录补齐完整结果。
func (c *Client) GetSourceTree(ctx context.Context, commitSHA string) (SourceTree, error) {
	commitSHA = strings.TrimSpace(commitSHA)
	if commitSHA == "" {
		return SourceTree{}, fmt.Errorf("源码树提交哈希不能为空")
	}
	treeSHA, err := c.fetchCommitTreeSHA(ctx, commitSHA)
	if err != nil {
		return SourceTree{}, err
	}
	recursive, err := c.fetchSourceTree(ctx, treeSHA, true)
	if err != nil {
		return SourceTree{}, err
	}
	if !recursive.Truncated {
		return recursive, nil
	}

	type pendingTree struct {
		prefix string
		sha    string
	}
	queue := []pendingTree{{sha: treeSHA}}
	entries := make([]SourceTreeEntry, 0, len(recursive.Entries))
	visited := make(map[string]struct{})
	for len(queue) > 0 {
		if len(visited) >= maxSourceTreeDirectories {
			return SourceTree{}, fmt.Errorf("源码树目录数量超过上限")
		}
		current := queue[0]
		queue = queue[1:]
		if _, exists := visited[current.sha]; exists {
			continue
		}
		visited[current.sha] = struct{}{}
		page, err := c.fetchSourceTree(ctx, current.sha, false)
		if err != nil {
			return SourceTree{}, err
		}
		for _, entry := range page.Entries {
			entry.Path = path.Join(current.prefix, entry.Path)
			entries = append(entries, entry)
			if len(entries) > maxSourceTreeEntries {
				return SourceTree{}, fmt.Errorf("源码树条目数量超过上限")
			}
			if entry.Type == "tree" && entry.SHA != "" {
				queue = append(queue, pendingTree{prefix: entry.Path, sha: entry.SHA})
			}
		}
	}
	return SourceTree{Entries: entries, Truncated: false}, nil
}

func (c *Client) fetchCommitTreeSHA(ctx context.Context, commitSHA string) (string, error) {
	endpoint := strings.TrimRight(c.apiBaseURL, "/") + "/repos/" +
		url.PathEscape(c.owner) + "/" + url.PathEscape(c.repo) + "/git/commits/" + url.PathEscape(commitSHA)
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return "", fmt.Errorf("创建 GitHub 提交请求失败: %w", err)
	}
	c.fillHeaders(request)
	response, err := c.httpClient.Do(request)
	if err != nil {
		return "", fmt.Errorf("调用 GitHub 提交接口失败: %w", err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 1024*1024))
	if err != nil {
		return "", fmt.Errorf("读取 GitHub 提交响应失败: %w", err)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return "", fmt.Errorf("GitHub 提交查询失败: HTTP %d", response.StatusCode)
	}
	var payload struct {
		Tree struct {
			SHA string `json:"sha"`
		} `json:"tree"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return "", fmt.Errorf("解析 GitHub 提交响应失败: %w", err)
	}
	treeSHA := strings.TrimSpace(payload.Tree.SHA)
	if treeSHA == "" {
		return "", fmt.Errorf("GitHub 提交未返回根树哈希")
	}
	return treeSHA, nil
}

func (c *Client) fetchSourceTree(ctx context.Context, treeish string, recursive bool) (SourceTree, error) {
	endpoint := strings.TrimRight(c.apiBaseURL, "/") + "/repos/" +
		url.PathEscape(c.owner) + "/" + url.PathEscape(c.repo) + "/git/trees/" + url.PathEscape(treeish)
	if recursive {
		endpoint += "?recursive=1"
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return SourceTree{}, fmt.Errorf("创建 GitHub 源码树请求失败: %w", err)
	}
	c.fillHeaders(request)
	response, err := c.httpClient.Do(request)
	if err != nil {
		return SourceTree{}, fmt.Errorf("调用 GitHub 源码树失败: %w", err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 32*1024*1024))
	if err != nil {
		return SourceTree{}, fmt.Errorf("读取 GitHub 源码树响应失败: %w", err)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return SourceTree{}, fmt.Errorf("GitHub 源码树失败: HTTP %d", response.StatusCode)
	}
	var payload struct {
		Tree      []SourceTreeEntry `json:"tree"`
		Truncated bool              `json:"truncated"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return SourceTree{}, fmt.Errorf("解析 GitHub 源码树响应失败: %w", err)
	}
	return SourceTree{Entries: payload.Tree, Truncated: payload.Truncated}, nil
}
