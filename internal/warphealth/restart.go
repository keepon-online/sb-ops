package warphealth

import (
	"context"
	"fmt"
	"time"
)

// 服务重启的复测轮询节奏：warp-svc 冷启动（停旧进程 + 起 daemon + 自动重连）
// 比 warp-cli 断开重连慢，宽限窗口给到换出口的两倍以上。用 var 仅为测试可调。
var (
	restartProbeAttempts = 8
	restartProbeInterval = 2 * time.Second
)

// RestartService 重启 warp-svc 系统服务，并在宽限窗口内滚动复测链路，复测
// 语义与 Rotate 一致。对症场景：warp-svc 半死/崩溃循环（warp-cli IPC 一并
// 超时）时换出口注定失败，真正的恢复手段是重启服务——2026-10-21 与
// 2026-11-16 两次线上实例均为 systemctl restart warp-svc 恢复。restart 由
// 装配层注入（root 部署直接 systemctl，包内不 exec，保持可测）；重启对链路
// 是数秒级中断，由前端二次确认。
func RestartService(ctx context.Context, restart func(context.Context) error, proxyAddr string, state *State) (Result, error) {
	if err := restart(ctx); err != nil {
		return Result{}, fmt.Errorf("重启 warp-svc 失败: %w", err)
	}
	return probeUntilTerminal(ctx, proxyAddr, state, restartProbeAttempts, restartProbeInterval, "重启后链路复测未通过")
}
