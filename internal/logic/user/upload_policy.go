package user

// 本文件留作 package user 内部对 shared/uploadpolicy 的"再导出别名"。
// 业务实现统一迁到 internal/logic/shared/uploadpolicy(打破 user ⇄ shared/uploadtoken 循环依赖),
// 这里保留别名让 user 包内的 logic(post_create / shop_create / get_upload_token)
// 仍按原来的 `user.FuncName` 调用方式工作, 不必逐一改 import。

import (
	"local-life-api/internal/logic/shared/uploadpolicy"
)

// 类型再导出
type FileTypePolicy = uploadpolicy.FileTypePolicy

// 变量再导出
var (
	FileTypePolicies = uploadpolicy.FileTypePolicies
	ValidBizs        = uploadpolicy.ValidBizs
)

// 函数再导出
var (
	IsValidBiz             = uploadpolicy.IsValidBiz
	ResolveFileTypePolicy  = uploadpolicy.ResolveFileTypePolicy
	BuildFileKeyDir        = uploadpolicy.BuildFileKeyDir
	ValidateUploadSize     = uploadpolicy.ValidateUploadSize
	FileTypeToAllowedExts  = uploadpolicy.FileTypeToAllowedExts
	ValidateAndCleanImages = uploadpolicy.ValidateAndCleanImages
	ValidateSingleImage    = uploadpolicy.ValidateSingleImage
	BuildURLsInJSON        = uploadpolicy.BuildURLsInJSON
)

// 常量再导出(biz 常量在 uploadpolicy 里已定义, user 包内可以直接用 uploadpolicy.BizPost,
// 但为了不破坏 package user 内部的 BizPost/BizShop/BizAvatar/BizBanner/BizNotice 引用,
// 这里再导出)
const (
	BizPost   = uploadpolicy.BizPost
	BizShop   = uploadpolicy.BizShop
	BizAvatar = uploadpolicy.BizAvatar
	BizBanner = uploadpolicy.BizBanner
	BizNotice = uploadpolicy.BizNotice
)
