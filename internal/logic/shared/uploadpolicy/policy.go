// Package uploadpolicy 给两侧 HTTP handler 共用的"OSS 上传业务策略"。
//   - 内容: biz 常量 + 合法性校验 + file key dir 拼装 + file_type 策略表 + 图片 URL 校验
//   - 放在 shared 子包:user / admin 两侧的 logic 各自 import,不允许依赖上层 logic
package uploadpolicy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"local-life-api/common/oss"
	"local-life-api/internal/errorx"
)

// FileTypePolicy 业务文件类型 → 后缀白名单 + 大小上限
//   - 业务侧根据这个映射做"前端能不能传"的策略校验
//   - OSS 包本身不接这套策略
type FileTypePolicy struct {
	// AllowedExts 允许的文件后缀(不带点)
	AllowedExts []string
	// MaxSize 字节上限;<=0 表示不限制
	MaxSize int64
}

// FileTypePolicies file_type → 业务策略
var FileTypePolicies = map[string]FileTypePolicy{
	"image": {
		AllowedExts: []string{"jpg", "jpeg", "png", "webp", "gif"},
		MaxSize:     10 * 1024 * 1024, // 10MB
	},
	"video": {
		AllowedExts: []string{"mp4", "mov", "m4v"},
		MaxSize:     100 * 1024 * 1024, // 100MB
	},
	"avatar": {
		AllowedExts: []string{"jpg", "jpeg", "png", "webp"},
		MaxSize:     5 * 1024 * 1024, // 5MB
	},
}

// Biz 业务域常量(文件 key 的一级目录)
const (
	BizPost   = "post"   // 帖子图片
	BizShop   = "shop"   // 商家证照
	BizAvatar = "avatar" // 头像
	BizBanner = "banner" // 首页轮播图(平台资源)
	BizNotice = "notice" // 平台公告(平台资源)
)

// ValidBizs 合法的 biz 集合
var ValidBizs = map[string]struct{}{
	BizPost:   {},
	BizShop:   {},
	BizAvatar: {},
	BizBanner: {},
	BizNotice: {},
}

// IsValidBiz 判断 biz 是否合法
func IsValidBiz(biz string) bool {
	_, ok := ValidBizs[biz]
	return ok
}

// ResolveFileTypePolicy 拿 file_type 对应的业务策略
func ResolveFileTypePolicy(fileType string) (FileTypePolicy, error) {
	p, ok := FileTypePolicies[fileType]
	if !ok {
		return FileTypePolicy{}, fmt.Errorf("%w: fileType=%s", errorx.ErrInvalidParam, fileType)
	}
	return p, nil
}

// BuildFileKeyDir 按 biz + uid 拼 file key 前缀
func BuildFileKeyDir(biz string, uid int64) (string, error) {
	if !IsValidBiz(biz) {
		return "", fmt.Errorf("%w: biz=%s", errorx.ErrInvalidParam, biz)
	}
	switch biz {
	case BizBanner, BizNotice:
		return fmt.Sprintf("%s/0/", biz), nil
	default:
		if uid <= 0 {
			return "", fmt.Errorf("%w: biz=%s 必须传 uid", errorx.ErrInvalidParam, biz)
		}
		return fmt.Sprintf("%s/%d/", biz, uid), nil
	}
}

// ValidateUploadSize 业务大小校验
func ValidateUploadSize(fileType string, size int64) error {
	if size <= 0 {
		return nil
	}
	policy, err := ResolveFileTypePolicy(fileType)
	if err != nil {
		return err
	}
	if policy.MaxSize > 0 && size > policy.MaxSize {
		return fmt.Errorf("%w: %s 超过 %d 字节", errorx.ErrInvalidParam, fileType, policy.MaxSize)
	}
	return nil
}

// FileTypeToAllowedExts 取 file_type 对应的后缀白名单
func FileTypeToAllowedExts(fileType string) ([]string, error) {
	policy, err := ResolveFileTypePolicy(fileType)
	if err != nil {
		return nil, err
	}
	return policy.AllowedExts, nil
}

// ===== 图片 URL 入库校验(公共, 所有 logic 复用) =====

// maxImagesPerPost 单图集最大图片数
const maxImagesPerPost = 9

// ValidateAndCleanImages 校验并清洗 images 字段(入库前的最后一道关)
//   - 入参 raw 是前端传的 JSON 数组字符串, 例 `["url1","url2"]`
//   - biz          决定 path prefix 允许范围
//   - uid          个人资源必传 uid, 平台资源传 0
//   - allowedExts  业务后缀白名单
//   - cfg          从 svcCtx 拿, 含 host 白名单 + 拼 host 的元数据
func ValidateAndCleanImages(raw string, biz string, uid int64, allowedExts []string, cfg oss.HostConfig) (string, error) {
	if raw == "" {
		return "", nil
	}
	if !IsValidBiz(biz) {
		return "", fmt.Errorf("%w: biz=%s", errorx.ErrInvalidParam, biz)
	}
	switch biz {
	case BizBanner, BizNotice:
		uid = 0
	default:
		if uid <= 0 {
			return "", fmt.Errorf("%w: biz=%s 必须传 uid", errorx.ErrInvalidParam, biz)
		}
	}

	var arr []any
	if err := json.Unmarshal([]byte(raw), &arr); err != nil {
		return "", fmt.Errorf("%w: images 不是合法 JSON: %v", errorx.ErrInvalidParam, err)
	}
	if arr == nil {
		return "", errors.New("images 必须是 JSON 数组")
	}
	if len(arr) == 0 || len(arr) > maxImagesPerPost {
		return "", fmt.Errorf("%w: images 数量 %d 超出范围 1-%d", errorx.ErrInvalidParam, len(arr), maxImagesPerPost)
	}

	allowedPrefixes := []string{fmt.Sprintf("%s/%d/", biz, uid)}
	cleaned := make([]string, 0, len(arr))
	for i, item := range arr {
		s, ok := item.(string)
		if !ok {
			return "", fmt.Errorf("%w: images[%d] 不是字符串", errorx.ErrInvalidParam, i)
		}
		okURL, err := oss.ValidateURL(s, cfg, allowedPrefixes, allowedExts)
		if err != nil {
			return "", fmt.Errorf("%w: images[%d]=%s: %v", errorx.ErrInvalidParam, i, s, err)
		}
		cleaned = append(cleaned, okURL)
	}

	out, err := json.Marshal(cleaned)
	if err != nil {
		return "", fmt.Errorf("images 序列化失败: %w", err)
	}
	return string(out), nil
}

// ValidateSingleImage 校验并清洗单张图片 URL(供 banner/avatar 等单图场景使用)
func ValidateSingleImage(raw string, biz string, uid int64, allowedExts []string, cfg oss.HostConfig) (string, error) {
	if raw == "" {
		return "", nil
	}
	if !IsValidBiz(biz) {
		return "", fmt.Errorf("%w: biz=%s", errorx.ErrInvalidParam, biz)
	}
	switch biz {
	case BizBanner, BizNotice:
		uid = 0
	default:
		if uid <= 0 {
			return "", fmt.Errorf("%w: biz=%s 必须传 uid", errorx.ErrInvalidParam, biz)
		}
	}
	allowedPrefixes := []string{fmt.Sprintf("%s/%d/", biz, uid)}
	return oss.ValidateURL(raw, cfg, allowedPrefixes, allowedExts)
}

// ===== 列表/详情里 file_key → URL 批量转换 =====

// BuildURLsInJSON 把 JSON 数组字符串里的 file_key 批量转成完整访问 URL
//   - 入参 rawJSON: DB 里的 JSON 数组字符串, 格式 "[\"url1\",\"url2\"]"
//     每个元素可以是 file_key("post/1/abc.jpg")也可以是完整 URL(自动提取 path)
//   - 入参 client:  oss 客户端(从 svcCtx.OSS.Client 拿), 用其 URL() 方法拼前缀
//   - 出参: 同样格式的 JSON 数组字符串, 每个元素都被换成完整 URL
//   - 入参为空 → 返回空字符串(让列表里无 URL 的项正常显示)
//   - 内部 JSON 解析失败时降级返回 "[]"(让列表里图片项空出来, 不阻断整接口)
//
// 调用方(列表/详情 logic):
//
//	resp.Images = BuildURLsInJSON(ctx, post.Images.String, l.svcCtx.OSS.Client)
func BuildURLsInJSON(_ context.Context, rawJSON string, client *oss.Client) string {
	if rawJSON == "" || rawJSON == "null" {
		return rawJSON
	}

	var keys []string
	if err := json.Unmarshal([]byte(rawJSON), &keys); err != nil {
		return "[]"
	}
	if len(keys) == 0 {
		return "[]"
	}

	urls := client.URLs(keys)

	out, err := json.Marshal(urls)
	if err != nil {
		return "[]"
	}
	return string(out)
}
