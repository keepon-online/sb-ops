package warphealth

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// ErrWarpNotDeployed 标记面板未部署 warp-masque 出站（端点据此回 404）。
var ErrWarpNotDeployed = errors.New("未部署 warp-masque 出站")

// 换出口的复测轮询节奏。用 var 而非 const 仅为测试可调；对外节奏类常量
// 仍按仓库约定集中在 const（勿进 config.yaml）。
var (
	rotateProbeAttempts = 5
	rotateProbeInterval = 1500 * time.Millisecond
)

// RunWarpCli 执行一条 warp-cli 子命令（装配层注入 sudo -n 包装，包内不直接
// exec，保持可测）。
type RunWarpCli func(ctx context.Context, args ...string) error

// Rotate 断开重连 WARP 换出口，并在宽限窗口内滚动复测链路，拿到终态即更新
// state 并返回。disconnect 失败不致命（可能本就未连接）；重连失败致命；
// 宽限期内 proxy-down（SOCKS 监听未起）与歧义失败（隧道还在握手）视作
// 未就绪继续等；宽限耗尽仍无终态时返回错误，最后一次确定性终态（若有）
// 会先落 state 驱动告警。
func Rotate(ctx context.Context, runWarpCli RunWarpCli, proxyAddr string, state *State) (Result, error) {
	_ = runWarpCli(ctx, "disconnect")
	if err := runWarpCli(ctx, "connect"); err != nil {
		return Result{}, fmt.Errorf("重连 WARP 失败: %w", err)
	}
	var lastErr error
	var lastResult Result
	for attempt := 1; ; attempt++ {
		result, err := Check(ctx, proxyAddr)
		if err == nil && result.Verdict != VerdictProxyDown {
			state.Record(result)
			return result, nil
		}
		if err != nil {
			lastErr = err
		} else {
			lastErr = fmt.Errorf("%s：%s", result.Verdict, result.Reason)
			lastResult = result
		}
		if attempt >= rotateProbeAttempts {
			break
		}
		select {
		case <-ctx.Done():
			return Result{}, ctx.Err()
		case <-time.After(rotateProbeInterval):
		}
	}
	if lastResult.Verdict != "" {
		state.Record(lastResult)
	}
	return Result{}, fmt.Errorf("换出口后链路复测未通过: %w", lastErr)
}
