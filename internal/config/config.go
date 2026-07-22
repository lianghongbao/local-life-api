package config

import (
	"github.com/zeromicro/go-zero/core/stores/cache"
	"github.com/zeromicro/go-zero/core/stores/sqlx"
	"github.com/zeromicro/go-zero/rest"
)

type Config struct {
	rest.RestConf

	DB         sqlx.SqlConf
	CacheRedis cache.CacheConf

	Auth struct {
		AccessSecret string
		AccessExpire int64
	}

	AdminAuth struct {
		AccessSecret string
		AccessExpire int64
	}

	RefreshSecret string
	RefreshExpire int64

	WxMini struct {
		AppID     string
		AppSecret string
	}

	OSS struct {
		Endpoint        string
		Bucket          string
		Region          string // bucket 所在地域,例 cn-hangzhou(V4 签名派生 + 拼 host 用)
		RoleArn         string // RAM 角色 ARN,STS AssumeRole 用
		AccessKeyID     string
		AccessKeySecret string

		// UploadHost 前端直传 OSS 用的目标地址(bucket 域名)
		//   例 https://<bucket>.<endpoint>
		//   留空则用 https://<bucket>.<endpoint> 兜底
		UploadHost string `json:",default="`
		// URLPrefix 资源访问 URL 前缀(指向 CDN 加速域名)
		//   例 https://<your-cdn-domain>
		//   入库 URL 和业务返回 URL 均基于此前缀拼 file_key
		URLPrefix string `json:",default="`

		// Hosts 入库图片 URL 允许的 host 列表(不含 scheme)
		//   用于 ValidateURL 时校验前端传入的 URL 不指向外网
		Hosts []string `json:",optional"`
	}

	MiniApp struct {
		MaxImagesPerPost int
		DefaultPageSize  int
		MaxPageSize      int
	}

	CommentLimit struct {
		CreatePerMin  int `json:",default=10"`
		LikePerMin    int `json:",default=60"`
		ReportPerHour int `json:",default=10"`
	}
	ReportThreshold int `json:",default=3"`

	// TencentMap 腾讯位置服务 reverse geocoding 凭证
	//   Key 申请: https://lbs.qq.com/dev/console/application/mine
	//   空字符串 / 占位符触发降级:logic 返回空字段,前端走"全市推荐"
	TencentMap struct {
		Key string `json:",optional"`
	}

	// DevMode 已被移除:所有接口严格走 JWT openid,无 openid 一律 401

	Cors CorsConf
}

// CorsConf CORS 跨域配置（从 yaml 读取）
type CorsConf struct {
	// Enabled 是否启用 CORS（生产同源部署可设为 false）
	Enabled bool `json:",default=false"`
	// AllowOrigins 允许的来源（精确匹配）
	AllowOrigins []string
	// AllowMethods 允许的 HTTP 方法
	AllowMethods []string
	// AllowHeaders 允许的请求头
	AllowHeaders []string
	// ExposeHeaders 暴露给浏览器的响应头
	ExposeHeaders []string
	// AllowCredentials 是否允许携带 cookie / Authorization
	AllowCredentials bool
	// MaxAge 预检请求缓存时间（秒）
	MaxAge int `json:",default=86400"`
}
