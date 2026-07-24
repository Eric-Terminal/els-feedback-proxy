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

const updateTimelinePageSize = 100

// UpdateTimelineCommit 是客户端检查更新所需的稳定提交摘要。
type UpdateTimelineCommit struct {
	OID             string    `json:"oid"`
	MessageHeadline string    `json:"message_headline"`
	Message         string    `json:"message"`
	CommittedAt     time.Time `json:"committed_at"`
	URL             string    `json:"url"`
	CIContexts      []string  `json:"ci_contexts"`
}

// UpdateTimeline 聚合固定仓库分支的提交时间线。
type UpdateTimeline struct {
	Version int                    `json:"version"`
	Branch  string                 `json:"branch"`
	Commits []UpdateTimelineCommit `json:"commits"`
}

// GetUpdateTimeline 使用服务端 GitHub 凭据分页读取提交和 CI 状态。
func (c *Client) GetUpdateTimeline(ctx context.Context, branch string, maxCount int) (UpdateTimeline, error) {
	branch = strings.TrimSpace(branch)
	if branch == "" {
		return UpdateTimeline{}, fmt.Errorf("检查更新分支不能为空")
	}
	if maxCount <= 0 {
		return UpdateTimeline{}, fmt.Errorf("检查更新提交数量必须大于零")
	}

	commits := make([]UpdateTimelineCommit, 0, min(maxCount, updateTimelinePageSize))
	var cursor *string

	for len(commits) < maxCount {
		pageSize := min(updateTimelinePageSize, maxCount-len(commits))
		page, err := c.fetchUpdateTimelinePage(ctx, branch, pageSize, cursor)
		if err != nil {
			return UpdateTimeline{}, err
		}
		commits = append(commits, page.Commits...)
		if !page.HasNextPage || page.EndCursor == "" || len(page.Commits) == 0 {
			break
		}
		cursor = &page.EndCursor
	}

	return UpdateTimeline{
		Version: 1,
		Branch:  branch,
		Commits: commits,
	}, nil
}

type updateTimelinePage struct {
	Commits     []UpdateTimelineCommit
	HasNextPage bool
	EndCursor   string
}

func (c *Client) fetchUpdateTimelinePage(
	ctx context.Context,
	branch string,
	pageSize int,
	cursor *string,
) (updateTimelinePage, error) {
	payload, err := json.Marshal(map[string]any{
		"query": updateTimelineGraphQLQuery,
		"variables": map[string]any{
			"owner":    c.owner,
			"repo":     c.repo,
			"branch":   branch,
			"pageSize": pageSize,
			"after":    cursor,
		},
	})
	if err != nil {
		return updateTimelinePage{}, fmt.Errorf("编码 GitHub 时间线请求失败: %w", err)
	}

	request, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		"https://api.github.com/graphql",
		bytes.NewReader(payload),
	)
	if err != nil {
		return updateTimelinePage{}, fmt.Errorf("创建 GitHub 时间线请求失败: %w", err)
	}
	c.fillHeaders(request)
	request.Header.Set("Content-Type", "application/json")

	response, err := c.httpClient.Do(request)
	if err != nil {
		return updateTimelinePage{}, fmt.Errorf("调用 GitHub 时间线失败: %w", err)
	}
	defer response.Body.Close()

	body, err := io.ReadAll(response.Body)
	if err != nil {
		return updateTimelinePage{}, fmt.Errorf("读取 GitHub 时间线响应失败: %w", err)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return updateTimelinePage{}, fmt.Errorf(
			"GitHub 时间线查询失败: HTTP %d, body=%s",
			response.StatusCode,
			string(body),
		)
	}

	var envelope updateTimelineGraphQLEnvelope
	if err := json.Unmarshal(body, &envelope); err != nil {
		return updateTimelinePage{}, fmt.Errorf("解析 GitHub 时间线响应失败: %w", err)
	}
	if len(envelope.Errors) > 0 {
		messages := make([]string, 0, len(envelope.Errors))
		for _, graphQLError := range envelope.Errors {
			if message := strings.TrimSpace(graphQLError.Message); message != "" {
				messages = append(messages, message)
			}
		}
		if len(messages) == 0 {
			messages = append(messages, "GitHub GraphQL 返回未知错误")
		}
		return updateTimelinePage{}, fmt.Errorf("GitHub 时间线查询失败: %s", strings.Join(messages, "; "))
	}

	history := envelope.Data.Repository.Ref.Target.History
	commits := make([]UpdateTimelineCommit, 0, len(history.Nodes))
	for _, node := range history.Nodes {
		contexts := make([]string, 0, len(node.StatusCheckRollup.Contexts.Nodes))
		seen := make(map[string]struct{}, len(node.StatusCheckRollup.Contexts.Nodes))
		for _, contextNode := range node.StatusCheckRollup.Contexts.Nodes {
			contextName := contextNode.displayName()
			if contextName == "" {
				continue
			}
			if _, exists := seen[contextName]; exists {
				continue
			}
			seen[contextName] = struct{}{}
			contexts = append(contexts, contextName)
		}
		commits = append(commits, UpdateTimelineCommit{
			OID:             node.OID,
			MessageHeadline: node.MessageHeadline,
			Message:         node.Message,
			CommittedAt:     node.CommittedDate,
			URL:             node.CommitURL,
			CIContexts:      contexts,
		})
	}

	return updateTimelinePage{
		Commits:     commits,
		HasNextPage: history.PageInfo.HasNextPage,
		EndCursor:   history.PageInfo.EndCursor,
	}, nil
}

type updateTimelineGraphQLEnvelope struct {
	Data struct {
		Repository struct {
			Ref struct {
				Target struct {
					History struct {
						PageInfo struct {
							HasNextPage bool   `json:"hasNextPage"`
							EndCursor   string `json:"endCursor"`
						} `json:"pageInfo"`
						Nodes []struct {
							OID               string    `json:"oid"`
							MessageHeadline   string    `json:"messageHeadline"`
							Message           string    `json:"message"`
							CommittedDate     time.Time `json:"committedDate"`
							CommitURL         string    `json:"commitUrl"`
							StatusCheckRollup struct {
								Contexts struct {
									Nodes []updateTimelineContextNode `json:"nodes"`
								} `json:"contexts"`
							} `json:"statusCheckRollup"`
						} `json:"nodes"`
					} `json:"history"`
				} `json:"target"`
			} `json:"ref"`
		} `json:"repository"`
	} `json:"data"`
	Errors []struct {
		Message string `json:"message"`
	} `json:"errors"`
}

type updateTimelineContextNode struct {
	Name       string `json:"name"`
	Context    string `json:"context"`
	CheckSuite struct {
		App struct {
			Name string `json:"name"`
		} `json:"app"`
		WorkflowRun struct {
			Workflow struct {
				Name string `json:"name"`
			} `json:"workflow"`
		} `json:"workflowRun"`
	} `json:"checkSuite"`
}

func (n updateTimelineContextNode) displayName() string {
	parts := []string{
		n.CheckSuite.App.Name,
		n.CheckSuite.WorkflowRun.Workflow.Name,
		n.Name,
		n.Context,
	}
	cleaned := make([]string, 0, len(parts))
	for _, part := range parts {
		if value := strings.TrimSpace(part); value != "" {
			cleaned = append(cleaned, value)
		}
	}
	return strings.Join(cleaned, " / ")
}

const updateTimelineGraphQLQuery = `
query UpdateTimeline(
  $owner: String!
  $repo: String!
  $branch: String!
  $pageSize: Int!
  $after: String
) {
  repository(owner: $owner, name: $repo) {
    ref(qualifiedName: $branch) {
      target {
        ... on Commit {
          history(first: $pageSize, after: $after) {
            pageInfo {
              hasNextPage
              endCursor
            }
            nodes {
              oid
              messageHeadline
              message
              committedDate
              commitUrl
              statusCheckRollup {
                contexts(first: 30) {
                  nodes {
                    ... on CheckRun {
                      name
                      checkSuite {
                        app {
                          name
                        }
                        workflowRun {
                          workflow {
                            name
                          }
                        }
                      }
                    }
                    ... on StatusContext {
                      context
                    }
                  }
                }
              }
            }
          }
        }
      }
    }
  }
}`
