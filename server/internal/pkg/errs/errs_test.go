package errs

import (
	"errors"
	"fmt"
	"testing"
)

func TestFromKeepsBusinessError(t *testing.T) {
	err := From(ErrBalanceNotEnough)
	if err.Code != CodeBalanceNotEnough {
		t.Fatalf("期望错误码 %d，实际 %d", CodeBalanceNotEnough, err.Code)
	}
	if err.HTTP != 402 {
		t.Errorf("钱包余额不足应返回 HTTP 402，实际 %d", err.HTTP)
	}
}

func TestFromWrapsUnknownError(t *testing.T) {
	err := From(errors.New("connection refused"))
	if err.Code != CodeInternal {
		t.Fatalf("未知错误应归为内部错误，实际 %d", err.Code)
	}
	if err.Err == nil {
		t.Error("应保留底层错误用于日志排查")
	}
	// 对外文案不能泄露底层细节
	if err.Msg == "connection refused" {
		t.Error("响应文案不应暴露底层错误信息")
	}
}

func TestWithMsgDoesNotMutateOriginal(t *testing.T) {
	original := ErrInvalidParam.Msg
	customized := ErrInvalidParam.WithMsg("手机号格式不正确，%d", 11)

	if customized.Msg != "手机号格式不正确，11" {
		t.Errorf("文案格式化失败: %s", customized.Msg)
	}
	if ErrInvalidParam.Msg != original {
		t.Errorf("WithMsg 污染了预定义错误: %s", ErrInvalidParam.Msg)
	}
}

func TestFromWrappedError(t *testing.T) {
	wrapped := fmt.Errorf("调用支付网关失败: %w", ErrPayFailed)
	if !Is(wrapped, CodePayFailed) {
		t.Fatal("errors.As 应能穿透 fmt.Errorf 包装识别业务错误")
	}
}
