package ports

import (
	"AgenticService/src/domain"
	"context"
)

// QuestionCoordinator 把工具的等待與 HTTP 回覆解耦。
//
// 與 ApprovalCoordinator 同樣的順序要求：Begin 必須先於事件送出，否則使用者
// 極快回覆時，答案會早於 waiter 註冊而遺失。
type QuestionCoordinator interface {
	Begin(domain.UserQuestion) error
	Wait(context.Context, string) (domain.UserQuestionAnswer, error)
	Answer(domain.UserQuestionAnswer) error
	Cancel(string)
}
