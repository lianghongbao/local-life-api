// 腾讯位置服务公共辅助
//   多个 logic(geo_reverse / geo_cities / geo_areas / health)共享 placeholder 判定

package user

// tencentMapKeyPlaceholders 所有代表"未配置"的字面量
//   - 显式占位符:旧 yaml 里的 TENCENT_MAP_KEY_PLACEHOLDER
//   - 未替换变量:${TENCENT_MAP_KEY}(环境变量未设置时,os.ExpandEnv 原样保留)
var tencentMapKeyPlaceholders = []string{
	"TENCENT_MAP_KEY_PLACEHOLDER",
	"${TENCENT_MAP_KEY}",
	"$TENCENT_MAP_KEY",
}

// isTencentMapKeyConfigured 检查 key 是否已经真实配置
//   - 空字符串 / 占位符 / 未替换的 ${VAR} 都视为未配置
func isTencentMapKeyConfigured(key string) bool {
	if key == "" {
		return false
	}
	for _, ph := range tencentMapKeyPlaceholders {
		if key == ph {
			return false
		}
	}
	return true
}