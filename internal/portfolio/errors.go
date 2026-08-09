package portfolio

import "errors"

// 领域哨兵错误。transport 层把它们映射成 HTTP 状态码。
var (
	ErrNotFound        = errors.New("portfolio: not found")
	ErrInvalidCode     = errors.New("portfolio: invalid stock code")
	ErrInvalidAction   = errors.New("portfolio: invalid action type")
	ErrInvalidPrice    = errors.New("portfolio: price must be positive")
	ErrInvalidShares   = errors.New("portfolio: shares must be positive")
	ErrOversell        = errors.New("portfolio: sell shares exceed holding")
	ErrBrokerInUse     = errors.New("portfolio: broker in use by holdings or actions")
	ErrDuplicateBroker = errors.New("portfolio: broker name already exists")
	ErrDuplicateName  = errors.New("portfolio: name already exists")
)
