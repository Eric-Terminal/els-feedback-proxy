package store

import (
	"strings"
	"testing"
)

func TestSurveyStorePersistsAnonymousResponsesAndFreezesDefinition(t *testing.T) {
	dataDir := t.TempDir()
	surveys, err := NewSurveyStore(dataDir)
	if err != nil {
		t.Fatalf("初始化意见征集存储失败: %v", err)
	}

	created, err := surveys.Create(testSurveyRecord())
	if err != nil {
		t.Fatalf("创建意见征集失败: %v", err)
	}
	response, err := surveys.Submit(created.Key, SurveyResponseInput{
		Answers: []SurveyAnswer{{
			QuestionID:        "design",
			SelectedOptionIDs: []string{"compact"},
			OtherText:         "希望列表更紧凑",
		}},
		Platform: "iOS",
		AppBuild: "120",
		Language: "zh-Hans",
	})
	if err != nil {
		t.Fatalf("保存匿名答卷失败: %v", err)
	}
	if response.Key == "" || response.SurveyKey != created.Key {
		t.Fatalf("答卷元数据不正确: %+v", response)
	}

	reloaded, err := NewSurveyStore(dataDir)
	if err != nil {
		t.Fatalf("重新加载意见征集存储失败: %v", err)
	}
	_, responses, err := reloaded.Results(created.Key)
	if err != nil {
		t.Fatalf("读取意见征集结果失败: %v", err)
	}
	if len(responses) != 1 || responses[0].Answers[0].OtherText != "希望列表更紧凑" {
		t.Fatalf("匿名答卷未正确持久化: %+v", responses)
	}

	replacement := created
	replacement.Title = "修改后的标题"
	if _, err := reloaded.Update(created.Key, replacement); err == nil ||
		!strings.Contains(err.Error(), "不能修改题目内容") {
		t.Fatalf("已有答卷后应冻结定义，实际错误: %v", err)
	}

	replacement = created
	replacement.Enabled = false
	if _, err := reloaded.Update(created.Key, replacement); err != nil {
		t.Fatalf("已有答卷后应允许停止发布: %v", err)
	}
	if err := reloaded.Delete(created.Key); err == nil {
		t.Fatal("已有答卷的意见征集不应允许删除")
	}
}

func TestSurveyStoreRejectsInvalidAnonymousResponse(t *testing.T) {
	surveys, err := NewSurveyStore(t.TempDir())
	if err != nil {
		t.Fatalf("初始化意见征集存储失败: %v", err)
	}
	created, err := surveys.Create(testSurveyRecord())
	if err != nil {
		t.Fatalf("创建意见征集失败: %v", err)
	}

	_, err = surveys.Submit(created.Key, SurveyResponseInput{
		Answers: []SurveyAnswer{{
			QuestionID:        "design",
			SelectedOptionIDs: []string{"unknown"},
		}},
	})
	if err == nil || !strings.Contains(err.Error(), "未知选项") {
		t.Fatalf("未知选项应被拒绝，实际错误: %v", err)
	}
}

func testSurveyRecord() SurveyRecord {
	return SurveyRecord{
		ID:          2026072401,
		Title:       "界面方案征集",
		Description: "选择更喜欢的界面方案。",
		Language:    "zh-Hans",
		Platform:    "iOS",
		Enabled:     true,
		Questions: []SurveyQuestion{{
			ID:         "design",
			Question:   "你更喜欢哪种布局？",
			Type:       "single_select",
			AllowOther: true,
			Required:   true,
			Options: []SurveyOption{
				{ID: "compact", Label: "紧凑布局"},
				{ID: "relaxed", Label: "宽松布局"},
			},
		}},
	}
}
