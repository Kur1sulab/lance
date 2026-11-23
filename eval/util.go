package eval

// 跨文件共用的小工具。

import (
	"crypto/sha256"
	"encoding/hex"
)

// hexDigest 返回字节串的 SHA256 十六进制表示。
func hexDigest(raw []byte) string {
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}
