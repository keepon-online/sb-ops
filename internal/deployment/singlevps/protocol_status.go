package singlevps

import (
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"strings"

	gopsnet "github.com/shirou/gopsutil/v3/net"

	runtimebox "github.com/keepon-online/sb-ops/internal/runtime/singbox"
	serviceprotocol "github.com/keepon-online/sb-ops/internal/service/protocol"
	"github.com/keepon-online/sb-ops/internal/service/secret"
)

// ListenSnapshot is the set of local ports that currently have a listening
// socket, split by transport. TCP ports come from LISTEN entries in
// /proc/net/tcp{,6}; UDP has no LISTEN state, so any bound socket counts.
type ListenSnapshot struct {
	TCP map[int]struct{}
	UDP map[int]struct{}
}

// ProtocolPortHopStatus reports whether the iptables REDIRECT rule for a
// port-hop protocol matches the desired range.
type ProtocolPortHopStatus struct {
	From int    `json:"from"`
	To   int    `json:"to"`
	Rule string `json:"rule"` // ok | missing | mismatched | unknown
}

type ProtocolRuntimeItem struct {
	ID                string                 `json:"id"`
	Name              string                 `json:"name"`
	Type              serviceprotocol.Type   `json:"type"`
	Transport         string                 `json:"transport"` // tcp | udp
	Enabled           bool                   `json:"enabled"`
	ListenPort        int                    `json:"listenPort"`
	Status            string                 `json:"status"`            // listening | not_listening | kernel_down | disabled | unknown
	ActiveConnections *int                   `json:"activeConnections"` // nil = 连接观测不可用
	PortHop           *ProtocolPortHopStatus `json:"portHop,omitempty"`
}

type ProtocolRuntimeStatusResult struct {
	KernelStatus         string                `json:"kernelStatus"`
	ConfigStatus         string                `json:"configStatus"`
	ListeningAvailable   bool                  `json:"listeningAvailable"`
	ListeningReason      string                `json:"listeningReason,omitempty"`
	ConnectionsAvailable bool                  `json:"connectionsAvailable"`
	ConnectionsReason    string                `json:"connectionsReason,omitempty"`
	Items                []ProtocolRuntimeItem `json:"items"`
}

// portHopRuleSpec is one parsed (or desired) sb-port-hop iptables rule.
type portHopRuleSpec struct {
	ID         string
	ListenPort int
	From       int
	To         int
}

// ProtocolRuntimeStatus answers, per protocol, whether its listen port is
// actually bound by the kernel and how many client connections are active.
// Every probe degrades gracefully: a failing /proc snapshot, Clash API or
// iptables read marks the corresponding signal unavailable instead of failing
// the whole request. Only the protocol list itself can fail hard (DB error).
func (s *SyncService) ProtocolRuntimeStatus(ctx context.Context) (ProtocolRuntimeStatusResult, error) {
	store := serviceprotocol.NewStore(s.db, secret.NewStore(s.db))
	protocols, err := store.List(ctx)
	if err != nil {
		return ProtocolRuntimeStatusResult{}, fmt.Errorf("加载协议列表失败: %w", err)
	}
	probe := s.Probe(ctx)
	kernel := s.kernelState()
	kernelStatus := kernel.State // running | stopped
	if kernel.State != "running" && kernel.Reason != "" {
		kernelStatus = "unhealthy"
	}
	result := ProtocolRuntimeStatusResult{
		KernelStatus: kernelStatus,
		ConfigStatus: probe.ConfigStatus,
		Items:        make([]ProtocolRuntimeItem, 0, len(protocols)),
	}

	snapshot, err := s.listenSnapshot(ctx)
	if err != nil {
		result.ListeningReason = fmt.Sprintf("读取监听端口失败：%v", err)
	} else {
		result.ListeningAvailable = true
	}

	// connections by inbound tag; an unavailable Clash API leaves counts nil.
	connectionCounts := map[string]int{}
	connections, connErr := s.Connections(ctx)
	switch {
	case connErr != nil:
		result.ConnectionsReason = fmt.Sprintf("连接观测失败：%v", connErr)
	case !connections.Available:
		result.ConnectionsReason = connections.Reason
	default:
		result.ConnectionsAvailable = true
		for _, item := range connections.Items {
			if item.Inbound != "" {
				connectionCounts[item.Inbound]++
			}
		}
	}

	hopDesired := map[string]portHopRuleSpec{}
	for _, protocol := range protocols {
		if !protocol.Enabled || (protocol.Type != serviceprotocol.TypeHysteria2 && protocol.Type != serviceprotocol.TypeTUIC) {
			continue
		}
		from, to := settingInt(protocol, "portHopFrom"), settingInt(protocol, "portHopTo")
		if from <= 0 && to <= 0 {
			continue
		}
		hopDesired[protocol.ID] = portHopRuleSpec{ID: protocol.ID, ListenPort: protocol.ListenPort, From: from, To: to}
	}
	hopRules, hopAvailable := map[string]portHopRuleSpec{}, false
	if len(hopDesired) > 0 {
		if output, hopErr := s.portHopSnapshot(ctx); hopErr == nil {
			hopAvailable = true
			for _, rule := range parsePortHopRules(output) {
				hopRules[rule.ID] = rule
			}
		}
	}

	for _, protocol := range protocols {
		item := ProtocolRuntimeItem{
			ID: protocol.ID, Name: protocol.Name, Type: protocol.Type, Enabled: protocol.Enabled,
			ListenPort: protocol.ListenPort, Transport: transportOf(protocol.Type),
		}
		ports := snapshot.TCP
		if item.Transport == "udp" {
			ports = snapshot.UDP
		}
		switch {
		case !protocol.Enabled:
			item.Status = "disabled"
		case kernel.State != "running":
			item.Status = "kernel_down"
		case !result.ListeningAvailable:
			item.Status = "unknown"
		case containsPort(ports, protocol.ListenPort):
			item.Status = "listening"
		default:
			item.Status = "not_listening"
		}
		if result.ConnectionsAvailable {
			count := connectionCounts[protocol.InboundTag()]
			item.ActiveConnections = &count
		}
		if desired, ok := hopDesired[protocol.ID]; ok {
			item.PortHop = &ProtocolPortHopStatus{From: desired.From, To: desired.To, Rule: "unknown"}
			switch {
			case !hopAvailable:
				// keep unknown
			default:
				actual, found := hopRules[desired.ID]
				switch {
				case !found:
					item.PortHop.Rule = "missing"
				case actual.From == desired.From && actual.To == desired.To && actual.ListenPort == desired.ListenPort:
					item.PortHop.Rule = "ok"
				default:
					item.PortHop.Rule = "mismatched"
				}
			}
		}
		result.Items = append(result.Items, item)
	}
	return result, nil
}

func (s *SyncService) listenSnapshot(ctx context.Context) (ListenSnapshot, error) {
	if s.ListenSnapshot != nil {
		return s.ListenSnapshot(ctx)
	}
	return defaultListenSnapshot(ctx)
}

func (s *SyncService) portHopSnapshot(ctx context.Context) (string, error) {
	if s.PortHopSnapshot != nil {
		return s.PortHopSnapshot(ctx)
	}
	return defaultPortHopSnapshot(ctx)
}

func (s *SyncService) kernelState() runtimebox.Status {
	if s.KernelState != nil {
		return s.KernelState()
	}
	return s.runtime.Status()
}

func transportOf(value serviceprotocol.Type) string {
	if value == serviceprotocol.TypeHysteria2 || value == serviceprotocol.TypeTUIC {
		return "udp"
	}
	return "tcp"
}

func containsPort(ports map[int]struct{}, port int) bool {
	_, ok := ports[port]
	return ok
}

func defaultListenSnapshot(ctx context.Context) (ListenSnapshot, error) {
	snapshot := ListenSnapshot{TCP: map[int]struct{}{}, UDP: map[int]struct{}{}}
	// "tcp" covers tcp4+tcp6 and "udp" covers udp4+udp6, so dual-stack listens
	// on :: are included.
	tcpValues, err := gopsnet.ConnectionsWithContext(ctx, "tcp")
	if err != nil {
		return ListenSnapshot{}, fmt.Errorf("读取 TCP 监听表失败: %w", err)
	}
	for _, value := range tcpValues {
		if value.Status == "LISTEN" && value.Laddr.Port > 0 && value.Laddr.Port <= 65535 {
			snapshot.TCP[int(value.Laddr.Port)] = struct{}{}
		}
	}
	udpValues, err := gopsnet.ConnectionsWithContext(ctx, "udp")
	if err != nil {
		return ListenSnapshot{}, fmt.Errorf("读取 UDP 绑定表失败: %w", err)
	}
	for _, value := range udpValues {
		if value.Laddr.Port > 0 && value.Laddr.Port <= 65535 {
			snapshot.UDP[int(value.Laddr.Port)] = struct{}{}
		}
	}
	return snapshot, nil
}

func defaultPortHopSnapshot(ctx context.Context) (string, error) {
	if _, err := exec.LookPath("iptables"); err != nil {
		return "", fmt.Errorf("未找到 iptables: %w", err)
	}
	out, err := exec.CommandContext(ctx, "iptables", "-t", "nat", "-S", "PREROUTING").Output()
	if err != nil {
		return "", fmt.Errorf("读取 iptables 规则失败: %w", err)
	}
	return string(out), nil
}

// parsePortHopRules extracts the sb-port-hop managed REDIRECT rules from an
// "iptables -t nat -S PREROUTING" dump.
func parsePortHopRules(output string) []portHopRuleSpec {
	rules := []portHopRuleSpec{}
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if !strings.Contains(line, "sb-port-hop-") {
			continue
		}
		spec := portHopRuleSpec{}
		fields := strings.Fields(line)
		for i := 0; i < len(fields); i++ {
			if i+1 >= len(fields) {
				break
			}
			switch fields[i] {
			case "--dport":
				spec.From, spec.To = parsePortRange(fields[i+1])
			case "--to-ports":
				spec.ListenPort, _ = strconv.Atoi(fields[i+1])
			case "--comment":
				spec.ID = strings.TrimPrefix(strings.Trim(fields[i+1], `"`), "sb-port-hop-")
			}
		}
		if spec.ID != "" {
			rules = append(rules, spec)
		}
	}
	return rules
}

func parsePortRange(value string) (int, int) {
	from, to, found := strings.Cut(value, ":")
	if !found {
		port, err := strconv.Atoi(from)
		if err != nil {
			return 0, 0
		}
		return port, port
	}
	fromPort, fromErr := strconv.Atoi(from)
	toPort, toErr := strconv.Atoi(to)
	if fromErr != nil || toErr != nil {
		return 0, 0
	}
	return fromPort, toPort
}
