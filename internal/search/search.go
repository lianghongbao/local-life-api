// Package search 提供关键词搜索的公共能力 —— MySQL FULLTEXT(ngram) 检索
//
// 检索方案:MySQL 8.0 原生 FULLTEXT + ngram 解析器
//   - 为什么不用 Elasticsearch:当前帖子量级极小(个位数~十万级),
//     ES 引入部署/监控/双写一致性成本不划算;MySQL 原生够用。
//   - 后续真要上 ES,只需替换本包的 SQL 构造部分,调用方契约不变。
//
// 索引定义见 docs/sql/migrations/2026-11-18-add-search-fulltext.sql:
//   - post_info: FULLTEXT ft_post_search (title, content) WITH PARSER ngram
//   - shop:      FULLTEXT ft_shop_search (name, address, description) WITH PARSER ngram
//
// ngram 的已知行为(决定了本包的关键词处理策略):
//   - ngram 按 2-gram 切分,所以"出租"会同时命中「房屋出租」和「招聘」(都含"出"字)
//   - 单字查询几乎全库命中(每个 2-gram 都被切开),噪音极大
//   → 因此本包要求关键词最短 2 字符,单字直接拒绝搜索
package search

import (
	"crypto/sha1"
	"encoding/hex"
	"strings"
	"unicode"
)

// ====== 关键词处理常量 ======

const (
	// MinKeywordRunes 关键词最短字符数(按 rune 计,不是字节)
	//   - ngram 解析器把词切成 2-gram,单字查询会命中几乎所有行,噪音远大于价值
	//   - 前端也应该禁掉单字提交,后端这里是兜底
	MinKeywordRunes = 2

	// MaxKeywordRunes 关键词最长字符数
	//   - 超长关键词对 ngram 匹配没有额外收益,徒增索引扫描
	//   - 同时防止超长串进缓存 key 撑爆 Redis
	MaxKeywordRunes = 64

	// CacheKeyHashLen 缓存 key 里关键词哈希的长度(十六进制字符数)
	//   - 原始关键词可能含中文/空格/特殊字符,直接拼进 Redis key 不安全(冒号、换行等会被误解)
	//   - 用哈希既避免 key 注入,又保证长度可控
	CacheKeyHashLen = 12
)

// ====== 关键词规范化 ======

// Normalize 规范化用户输入的关键词
//   - 去首尾空白
//   - 连续空白折叠成一个空格(全角空格也一并处理)
//   - 长度裁剪到 [MinKeywordRunes, MaxKeywordRunes] 允许的范围内
//   - 返回空字符串表示"不搜索"
func Normalize(raw string) string {
	// 全角空格(U+3000)统一成半角空格,否则会被当成词内字符参与匹配
	s := strings.ReplaceAll(raw, "　", " ")
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	// 折叠连续空白
	s = strings.Join(strings.Fields(s), " ")

	// 按 rune 裁剪,避免把 UTF-8 字符切半(切半会产生乱码且破坏 ngram)
	runes := []rune(s)
	if len(runes) > MaxKeywordRunes {
		runes = runes[:MaxKeywordRunes]
	}
	return string(runes)
}

// IsValid 判断规范化后的关键词是否可以用于搜索
//   - 空串:不搜索
//   - 少于 MinKeywordRunes 个 rune:不搜索(ngram 单字噪音太大)
func IsValid(keyword string) bool {
	return len([]rune(keyword)) >= MinKeywordRunes
}

// TooShort 判断关键词是否"有内容但太短"(少于 MinKeywordRunes 个 rune)
//
//   为什么要单独区分「太短」和「没输关键词」:
//     - 没输关键词 → 正常浏览列表(返回全部)
//     - 输了但太短 → 用户明显是想搜索,却因为 ngram 单字匹配不到任何东西
//       (MySQL ngram 默认 token_size=2,单字查询恒返回空)
//       此时若也返回全部,用户会以为「搜索没生效」
//       → 应返回空列表,由前端提示「关键词至少 2 个字」
func TooShort(keyword string) bool {
	return keyword != "" && len([]rune(keyword)) < MinKeywordRunes
}

// ====== BOOLEAN MODE 查询词构造 ======

// BuildBooleanQuery 构造 MATCH ... AGAINST (... IN BOOLEAN MODE) 的查询串
//   - keyword: 已规范化的关键词
//   - 返回: BOOLEAN 模式查询串
//
// 权重语法:
//
//	+word    必须包含
//	-word    必须不包含
//	word*    通配符(前缀匹配)
//	>word    提高权重
//	<word    降低权重
//
// ngram 解析器下的处理策略:
//   - 标题命中的权重高于正文,这里用 ">标题词" 的思路做加权排序
//   - 关键词含空格时,按词拆开用 "+" 要求全部命中(AND 语义,更精准)
//   - 纯中文连续词(如"自行车")不能拆,拆了就变成"自行"+"行车",反而召回变差
//     → 只有当原词含空格时才拆;否则整词作为一个必含词
func BuildBooleanQuery(keyword string) string {
	fields := strings.Fields(keyword)
	if len(fields) <= 1 {
		// 单个词:原样必含。ngram 内部会自行切 2-gram
		return "+" + escapeBooleanToken(keyword)
	}
	// 多词:全部必含(AND),任一词缺失则该行不命中
	var sb strings.Builder
	for _, f := range fields {
		sb.WriteString("+")
		sb.WriteString(escapeBooleanToken(f))
		sb.WriteString(" ")
	}
	return strings.TrimSpace(sb.String())
}

// escapeBooleanToken 转义 BOOLEAN MODE 中的运算符字符
//   - BOOLEAN 模式里 + - > < ~ * " ( ) 都有特殊含义
//   - 用户输入的词里若含这些字符,必须转义,否则会变成运算符改变语义
//   - 用反斜杠前缀转义
func escapeBooleanToken(token string) string {
	var sb strings.Builder
	for _, r := range token {
		switch r {
		case '+', '-', '>', '<', '~', '*', '"', '(', ')', '\\':
			sb.WriteRune('\\')
		}
		sb.WriteRune(r)
	}
	return sb.String()
}

// ====== 缓存 key 辅助 ======

// CacheKeyHash 计算关键词的缓存 key 哈希
//   - keyword: 已规范化的关键词
//   - 返回: 定长十六进制字符串(不足则补零,超长则截断)
//
// 为什么不直接用原始关键词拼 key:
//   - 关键词含中文/空格/冒号等字符,直接拼进 Redis key 会有歧义和注入风险
//   - 长度不可控,长关键词会撑大 key
//   - 相同关键词必须产生相同 hash(缓存命中),所以用 sha1 而非随机数
func CacheKeyHash(keyword string) string {
	if keyword == "" {
		return ""
	}
	sum := sha1.Sum([]byte(keyword))
	h := hex.EncodeToString(sum[:])
	if len(h) > CacheKeyHashLen {
		return h[:CacheKeyHashLen]
	}
	return h
}

// ====== 展示用高亮辅助 ======

// ContainsAllRunes 判断关键词的每个词是否都出现在目标文本中(用于前端高亮前的预判)
//   - keyword: 已规范化的关键词
//   - text: 目标文本
//   - 纯字符串包含判断,不做 ngram 匹配,所以比 MATCH 更严格
func ContainsAllRunes(keyword string, text string) bool {
	if keyword == "" {
		return true
	}
	for _, f := range strings.Fields(keyword) {
		if !strings.Contains(text, f) {
			return false
		}
	}
	return true
}

// RuneLen 返回字符串的 rune 长度(中文按 1 个字符算)
func RuneLen(s string) int {
	return len([]rune(s))
}

// HasCJK 判断字符串是否包含中文字符
//   - 用于日志/调试:中文字符走 ngram,纯 ASCII 走单词切分
func HasCJK(s string) bool {
	for _, r := range s {
		if unicode.Is(unicode.Han, r) {
			return true
		}
	}
	return false
}
