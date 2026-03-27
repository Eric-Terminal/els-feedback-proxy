package moderation

import "context"

// ReviewInput 表示待审核的反馈文本。
type ReviewInput struct {
	Type              string
	Title             string
	Detail            string
	ReproductionSteps string
	ExpectedBehavior  string
	ActualBehavior    string
	ExtraContext      string
}

// Decision 表示审核结论。
type Decision struct {
	Allow      bool
	Reasons    []string
	Categories []string
	Confidence float64
}

// Reviewer 定义审核器能力。
type Reviewer interface {
	Review(ctx context.Context, input ReviewInput) (Decision, error)
}

// AllowAllReviewer 在禁用审核时直接放行。
type AllowAllReviewer struct{}

func (AllowAllReviewer) Review(_ context.Context, _ ReviewInput) (Decision, error) {
	return Decision{
		Allow:      true,
		Reasons:    []string{"审核已禁用，默认放行"},
		Categories: []string{},
		Confidence: 1,
	}, nil
}
