package argo

import (
	"context"
	"strings"
	"testing"
)

// TestSafeReconcileRecoversPanic：reconcile 首行即触达 store，nil store 必然
// panic——safeReconcile 必须恢复并把堆栈记进日志，而不是带崩同进程的面板。
func TestSafeReconcileRecoversPanic(t *testing.T) {
	var logs []string
	manager := NewManager(nil, nil, "", func(level, message string) {
		logs = append(logs, level+" "+message)
	}, nil)

	manager.safeReconcile(context.Background())

	caught := false
	for _, entry := range logs {
		if level := strings.Fields(entry)[0]; level != "error" {
			t.Errorf("panic 恢复日志级别应为 error, got %q in %q", level, entry)
		}
		if strings.Contains(entry, "panic") && strings.Contains(entry, "goroutine") {
			caught = true
		}
	}
	if !caught {
		t.Fatalf("panic 应被捕获并记录（含堆栈）, logs=%v", logs)
	}
}
