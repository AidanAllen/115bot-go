package p115

import (
	"context"
	"errors"
	"testing"
	"time"
)

// TestRetryStopsOnSuccess 首次成功就不该再重试。
func TestRetryStopsOnSuccess(t *testing.T) {
	calls := 0
	err := retry(context.Background(), 3, time.Millisecond, func() error {
		calls++
		return nil
	})
	if err != nil || calls != 1 {
		t.Fatalf("期望 1 次调用且成功，实际 calls=%d err=%v", calls, err)
	}
}

// TestRetryExhaustsAttempts 全部失败时返回最后一次错误，且次数受上限约束。
func TestRetryExhaustsAttempts(t *testing.T) {
	calls := 0
	want := errors.New("boom")
	err := retry(context.Background(), 3, time.Millisecond, func() error {
		calls++
		return want
	})
	if !errors.Is(err, want) {
		t.Fatalf("期望返回最后一次错误，实际 %v", err)
	}
	if calls != 3 {
		t.Fatalf("期望调用 3 次，实际 %d", calls)
	}
}

// TestRetryAbortsOnCancel ctx 取消后不再重试，避免关机时还要等退避。
func TestRetryAbortsOnCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	calls := 0
	err := retry(ctx, 5, time.Hour, func() error {
		calls++
		cancel()
		return errors.New("boom")
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("期望 context.Canceled，实际 %v", err)
	}
	if calls != 1 {
		t.Fatalf("取消后不应再调用，实际 %d 次", calls)
	}
}
