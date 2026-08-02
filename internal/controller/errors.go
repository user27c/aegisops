package controller

import (
	"errors"
	"time"
)

// ErrTransient 是可重试的暂时性错误（网络抖动等）。
// Reconcile 收到后保持当前 Phase 并指数退避 requeue。
var ErrTransient = errors.New("transient error")

// transientRequeueAfter 返回指数退避间隔（attempt 0 起：30s、60s、120s…上限 5min）。
func transientRequeueAfter(attempt int) time.Duration {
	multiplier := 1 << min(attempt, 4) // 2^attempt, 上限 16
	d := 30 * time.Second * time.Duration(multiplier)
	if d > 5*time.Minute {
		d = 5 * time.Minute
	}
	return d
}
