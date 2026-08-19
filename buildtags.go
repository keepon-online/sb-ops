//go:build !with_quic || !with_utls || !with_clash_api || !with_v2ray_api || !with_wireguard || !with_gvisor

package main

// 此文件在缺少 sing-box 必需构建标签时让编译直接失败，避免得到一个运行期才报错的
// 「残缺」二进制。内嵌的 sing-box 只有加上这些标签才会包含 QUIC（Hysteria2/TUIC）、
// uTLS（Reality）、Clash API、V2Ray API、WireGuard/WARP 出站与 gVisor（WireGuard
// 用户态设备栈）；缺标签时这些能力是空壳。
//
// 正确构建命令（任选其一）：
//
//	make build
//	make run
//	go run -tags=with_quic,with_utls,with_clash_api,with_v2ray_api,with_wireguard,with_gvisor ./
var _ = singBoxBuildTagsRequired_use_tags_with_quic_with_utls_with_clash_api_with_v2ray_api_with_wireguard_with_gvisor
