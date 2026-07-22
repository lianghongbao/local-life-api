package oss

import (
	"fmt"
	"strings"
)

// NormalizeExt 标准化文件后缀(去点 + 小写)
func NormalizeExt(ext string) string {
	return strings.ToLower(strings.TrimPrefix(ext, "."))
}

// ValidateExt 判断 ext 是否在白名单内
//   - allowedExts 为 nil/空时不做白名校验(由调用方决定)
func ValidateExt(ext string, allowedExts []string) error {
	ext = NormalizeExt(ext)
	if len(allowedExts) == 0 {
		return nil
	}
	for _, allow := range allowedExts {
		if NormalizeExt(allow) == ext {
			return nil
		}
	}
	return fmt.Errorf("%w: ext=%s", ErrInvalidExt, ext)
}
