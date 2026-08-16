package strategy

import "context"

// Analysis 一次结构化 AI 分析的持久化记录。
type Analysis struct {
	ID        int64   `json:"id"`
	Code      string  `json:"code"`
	Name      string  `json:"name"`
	Direction string  `json:"direction"`
	Advice    string  `json:"advice"`
	Trigger   string  `json:"trigger"`
	Risk      string  `json:"risk"`
	PriceAt   float64 `json:"price_at"`
	CreatedAt string  `json:"created_at"`
}

// AnalysisStore AI 分析历史的持久化端口。
type AnalysisStore interface {
	SaveAnalysis(ctx context.Context, a *Analysis) (int64, error)
	// ListAnalyses 返回某代码的分析历史（按时间倒序）；code 为空表示全部。
	ListAnalyses(ctx context.Context, code string) ([]Analysis, error)
}
