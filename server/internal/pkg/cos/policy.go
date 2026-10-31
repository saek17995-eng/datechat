package cos

import (
	"encoding/json"
	"fmt"
)

// Policy 是 POST Object 的安全策略。
//
// 字段顺序必须与文档一致：expiration 在前、conditions 在后——
// 策略文本参与签名计算，顺序不同会得到完全不同的签名。
type Policy struct {
	Expiration string `json:"expiration"`
	Conditions []any  `json:"conditions"`
}

// JSON 序列化策略文本。
// 不用 json.Marshal 的默认行为之外再加任何美化：签名对字节敏感，
// 缩进、字段顺序、HTML 转义的差异都会让签名校验失败。
func (p Policy) JSON() (string, error) {
	raw, err := json.Marshal(p)
	if err != nil {
		return "", fmt.Errorf("序列化失败: %w", err)
	}
	return string(raw), nil
}
