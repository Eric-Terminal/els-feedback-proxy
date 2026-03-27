package moderation

import (
	"context"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestOpenAIReviewerReviewSuccess(t *testing.T) {
	reviewer := newReviewerForTest(func(_ *http.Request) (*http.Response, error) {
		return mockResponse(http.StatusOK, `{"choices":[{"message":{"content":"{\"allow\":false,\"reasons\":[\"包含明显不健康内容\"],\"categories\":[\"色情\"],\"confidence\":0.91}"}}]}`), nil
	})

	decision, err := reviewer.Review(context.Background(), ReviewInput{
		Type:   "bug",
		Title:  "测试标题",
		Detail: "测试详细描述",
	})
	if err != nil {
		t.Fatalf("期望审核成功，实际失败: %v", err)
	}
	if decision.Allow {
		t.Fatalf("期望不通过，实际 allow=true")
	}
	if len(decision.Reasons) == 0 {
		t.Fatalf("期望包含 reasons")
	}
}

func TestOpenAIReviewerRetriesUntilSuccess(t *testing.T) {
	var calls atomic.Int32

	reviewer := newReviewerForTest(func(_ *http.Request) (*http.Response, error) {
		current := calls.Add(1)
		if current < 3 {
			return mockResponse(http.StatusOK, `{"choices":[{"message":{"content":"{\"allow\":\"invalid\"}"}}]}`), nil
		}
		return mockResponse(http.StatusOK, `{"choices":[{"message":{"content":"{\"allow\":true,\"reasons\":[\"正常反馈\"],\"categories\":[\"正常反馈\"],\"confidence\":0.82}"}}]}`), nil
	})

	decision, err := reviewer.Review(context.Background(), ReviewInput{
		Type:   "suggestion",
		Title:  "测试",
		Detail: "这是一个正常建议内容",
	})
	if err != nil {
		t.Fatalf("期望第三次成功，实际失败: %v", err)
	}
	if !decision.Allow {
		t.Fatalf("期望 allow=true")
	}
	if calls.Load() != 3 {
		t.Fatalf("期望调用 3 次，实际 %d", calls.Load())
	}
}

func TestOpenAIReviewerRetryExhausted(t *testing.T) {
	reviewer := newReviewerForTest(func(_ *http.Request) (*http.Response, error) {
		return mockResponse(http.StatusInternalServerError, `{"error":"server down"}`), nil
	})

	_, err := reviewer.Review(context.Background(), ReviewInput{
		Type:   "bug",
		Title:  "测试",
		Detail: "这是一个测试内容",
	})
	if err == nil {
		t.Fatalf("期望重试后仍失败")
	}
}

func newReviewerForTest(transport roundTripFunc) *OpenAIReviewer {
	reviewer := NewOpenAIReviewer(OpenAIReviewerConfig{
		BaseURL:     "https://moderation.test",
		APIKey:      "test-key",
		Model:       "test-model",
		Timeout:     5 * time.Second,
		MaxRetries:  3,
		Temperature: 0,
	})
	reviewer.httpClient = &http.Client{
		Timeout:   5 * time.Second,
		Transport: transport,
	}
	return reviewer
}

type roundTripFunc func(request *http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return fn(request)
}

func mockResponse(code int, body string) *http.Response {
	return &http.Response{
		StatusCode: code,
		Body:       io.NopCloser(strings.NewReader(body)),
		Header:     http.Header{"Content-Type": []string{"application/json"}},
	}
}
