package singlevps

import (
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"strings"

	serviceprotocol "github.com/keepon-online/sb-ops/internal/service/protocol"
)

// portHopRule is one desired iptables REDIRECT rule that maps a UDP port range
// onto a protocol's real listen port so clients can hop across the range.
type portHopRule struct {
	ID         string
	ListenPort int
	From       int
	To         int
}

// reconcilePortHop applies iptables REDIRECT rules for Hysteria2/TUIC protocols
// with a configured port-hop range. Rules are tagged with an "sb-port-hop-<id>"
// comment so only rules this service manages are touched. It is best-effort:
// when iptables is unavailable (no root / non-Linux), it returns an error that
// the caller records as a diagnostic without failing the sync.
func reconcilePortHop(ctx context.Context, protocols []serviceprotocol.Protocol) error {
	desired := []portHopRule{}
	for _, protocol := range protocols {
		if !protocol.Enabled || (protocol.Type != serviceprotocol.TypeHysteria2 && protocol.Type != serviceprotocol.TypeTUIC) {
			continue
		}
		from, to := settingInt(protocol, "portHopFrom"), settingInt(protocol, "portHopTo")
		if from <= 0 && to <= 0 {
			continue
		}
		desired = append(desired, portHopRule{ID: protocol.ID, ListenPort: protocol.ListenPort, From: from, To: to})
	}
	if _, err := exec.LookPath("iptables"); err != nil {
		if len(desired) == 0 {
			return nil
		}
		return fmt.Errorf("端口跳跃需要 iptables，但未找到: %w", err)
	}

	// Drop all previously managed rules, then re-add the desired set. Using the
	// saved -S spec for -D makes removal exact. When there is nothing desired,
	// an iptables read failure (non-root, missing netfilter) is tolerated so an
	// idle panel does not log noise.
	out, err := exec.CommandContext(ctx, "iptables", "-t", "nat", "-S", "PREROUTING").Output()
	if err != nil {
		if len(desired) == 0 {
			return nil
		}
		return fmt.Errorf("读取 iptables 规则失败: %w", err)
	}
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if !strings.Contains(line, "sb-port-hop-") {
			continue
		}
		spec := strings.TrimSpace(strings.TrimPrefix(line, "-A"))
		args := append([]string{"-t", "nat", "-D", "PREROUTING"}, strings.Fields(spec)...)
		if err := exec.CommandContext(ctx, "iptables", args...).Run(); err != nil {
			return fmt.Errorf("移除旧端口跳跃规则失败: %w", err)
		}
	}
	for _, rule := range desired {
		args := []string{"-t", "nat", "-A", "PREROUTING", "-p", "udp",
			"--dport", fmt.Sprintf("%d:%d", rule.From, rule.To),
			"-j", "REDIRECT", "--to-ports", strconv.Itoa(rule.ListenPort),
			"-m", "comment", "--comment", "sb-port-hop-" + rule.ID}
		if err := exec.CommandContext(ctx, "iptables", args...).Run(); err != nil {
			return fmt.Errorf("添加端口跳跃规则失败: %w", err)
		}
	}
	return nil
}

func settingInt(protocol serviceprotocol.Protocol, key string) int {
	switch value := protocol.Settings[key].(type) {
	case int:
		return value
	case int64:
		return int(value)
	case float64:
		return int(value)
	default:
		return 0
	}
}
