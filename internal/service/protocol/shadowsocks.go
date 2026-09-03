package protocol

import (
	"crypto/sha256"
	"encoding/base64"
	"strings"
)

// ShadowsocksUserPassword 从设备凭据派生 SS-2022 用户密钥（sha256 截断后
// Base64），供订阅编译与端到端拨测共用。多用户模式下内核按「服务端密钥 +
// 各用户密钥」校验，客户端密码必须是 "服务端密钥:用户密钥" 的冒号组合。
func ShadowsocksUserPassword(method, credential string) string {
	digest := sha256.Sum256([]byte(credential))
	keyLength := 32
	if strings.Contains(method, "aes-128") {
		keyLength = 16
	}
	return base64.StdEncoding.EncodeToString(digest[:keyLength])
}
