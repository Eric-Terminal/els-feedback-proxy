package github

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const sourceArchiveTimeout = 2 * time.Minute

// DownloadSourceArchive 打开精确 Commit 的 GitHub 归档流，由调用方负责关闭。
func (c *Client) DownloadSourceArchive(ctx context.Context, commitSHA string) (io.ReadCloser, error) {
	commitSHA = strings.TrimSpace(commitSHA)
	if commitSHA == "" {
		return nil, fmt.Errorf("源码归档提交哈希不能为空")
	}
	endpoint := strings.TrimRight(c.apiBaseURL, "/") + "/repos/" +
		url.PathEscape(c.owner) + "/" + url.PathEscape(c.repo) + "/tarball/" + url.PathEscape(commitSHA)
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("创建 GitHub 源码归档请求失败: %w", err)
	}
	c.fillHeaders(request)

	httpClient := *c.httpClient
	if httpClient.Timeout == 0 || httpClient.Timeout < sourceArchiveTimeout {
		httpClient.Timeout = sourceArchiveTimeout
	}
	response, err := httpClient.Do(request)
	if err != nil {
		return nil, fmt.Errorf("下载 GitHub 源码归档失败: %w", err)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		defer response.Body.Close()
		body, _ := io.ReadAll(io.LimitReader(response.Body, 4*1024))
		return nil, fmt.Errorf("GitHub 源码归档下载失败: HTTP %d, body=%s", response.StatusCode, string(body))
	}
	return response.Body, nil
}
