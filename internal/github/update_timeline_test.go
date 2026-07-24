package github

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestGetUpdateTimelineUsesAuthenticatedGraphQLAndMapsContexts(t *testing.T) {
	requestCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		requestCount++
		if request.Method != http.MethodPost {
			t.Fatalf("GitHub GraphQL 应使用 POST，实际 %s", request.Method)
		}
		if request.Header.Get("Authorization") != "Bearer test-token" {
			t.Fatalf("GitHub GraphQL 请求缺少服务端凭据")
		}
		if request.Header.Get("Content-Type") != "application/json" {
			t.Fatalf("GitHub GraphQL Content-Type 不正确")
		}

		var payload struct {
			Variables struct {
				Owner    string  `json:"owner"`
				Repo     string  `json:"repo"`
				Branch   string  `json:"branch"`
				PageSize int     `json:"pageSize"`
				After    *string `json:"after"`
			} `json:"variables"`
		}
		if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
			t.Fatalf("解析 GraphQL 请求失败: %v", err)
		}
		if payload.Variables.Owner != "owner" ||
			payload.Variables.Repo != "repo" ||
			payload.Variables.Branch != "dev" ||
			payload.Variables.PageSize != 2 ||
			payload.Variables.After != nil {
			t.Fatalf("GraphQL 变量不正确: %+v", payload.Variables)
		}

		response.Header().Set("Content-Type", "application/json")
		_, _ = response.Write([]byte(`{
			"data": {
				"repository": {
					"ref": {
						"target": {
							"history": {
								"pageInfo": {
									"hasNextPage": false,
									"endCursor": ""
								},
								"nodes": [
									{
										"oid": "abcdef1234567890",
										"messageHeadline": "feat: 测试",
										"message": "feat: 测试\n\n正文",
										"committedDate": "2026-07-24T08:00:00Z",
										"commitUrl": "https://github.com/owner/repo/commit/abcdef1234567890",
										"statusCheckRollup": {
											"contexts": {
												"nodes": [
													{
														"name": "构建",
														"checkSuite": {
															"app": {"name": "Xcode Cloud"},
															"workflowRun": {
																"workflow": {"name": "iOS"}
															}
														}
													},
													{"context": "签名检查"}
												]
											}
										}
									}
								]
							}
						}
					}
				}
			}
		}`))
	}))
	defer server.Close()

	client := NewClient("test-token", "owner", "repo")
	client.httpClient = server.Client()
	originalTransport := client.httpClient.Transport
	client.httpClient.Transport = rewriteGraphQLTransport{
		baseURL:   server.URL,
		transport: originalTransport,
	}

	timeline, err := client.GetUpdateTimeline(context.Background(), "dev", 2)
	if err != nil {
		t.Fatalf("获取 GitHub 时间线失败: %v", err)
	}
	if requestCount != 1 {
		t.Fatalf("单页时间线应只请求一次，实际 %d", requestCount)
	}
	if timeline.Version != 1 || timeline.Branch != "dev" || len(timeline.Commits) != 1 {
		t.Fatalf("时间线响应不正确: %+v", timeline)
	}
	commit := timeline.Commits[0]
	if commit.OID != "abcdef1234567890" ||
		len(commit.CIContexts) != 2 ||
		commit.CIContexts[0] != "Xcode Cloud / iOS / 构建" ||
		commit.CIContexts[1] != "签名检查" {
		t.Fatalf("提交或 CI 上下文映射不正确: %+v", commit)
	}
}

type rewriteGraphQLTransport struct {
	baseURL   string
	transport http.RoundTripper
}

func (t rewriteGraphQLTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	cloned := request.Clone(request.Context())
	target, _ := http.NewRequestWithContext(
		request.Context(),
		request.Method,
		t.baseURL,
		request.Body,
	)
	cloned.URL = target.URL
	return t.transport.RoundTrip(cloned)
}
