package svc

import (
	"github.com/zeromicro/go-zero/core/stores/redis"
	"github.com/zeromicro/go-zero/core/stores/sqlx"

	"local-life-api/common/oss"
	"local-life-api/internal/config"
	"local-life-api/internal/model"
)

// OSSBundle OSS 相关依赖的聚合
//   - Client: 上传/拼 URL 用的单例(内部带 STS 凭证本地缓存)
//   - HostCfg: 入库 URL 校验用的配置快照
type OSSBundle struct {
	Client  *oss.Client
	HostCfg oss.HostConfig
}

type ServiceContext struct {
	Config config.Config

	// 基础设施连接
	DBConn   sqlx.SqlConn
	RedisCli *redis.Redis

	// TencentMapKey 腾讯位置服务 reverse geocoding key
	//   空字符串 / 占位符触发 geoReverseLogic 降级(返回空字段)
	TencentMapKey string

	// OSS 相关依赖(单例 client + URL 校验配置)
	OSS OSSBundle

	// ===== 数据访问 Model（按表注入，业务 logic 通过 svcCtx 访问）=====
	UserModel     model.UserModel     // 用户表
	PostInfoModel model.PostInfoModel // 分类信息表
	ShopModel     model.ShopModel     // 商家黄页表
	NoticeModel   model.NoticeModel   // 公告表
	BannerModel   model.BannerModel   // 轮播图表
	AdminModel    model.AdminModel    // 管理员表

	// 评论
	CommentModel       model.CommentModel       // 评论主表
	CommentLikeModel   model.CommentLikeModel   // 评论点赞表
	CommentReportModel model.CommentReportModel // 评论举报表
}

func NewServiceContext(c config.Config) *ServiceContext {
	// 初始化数据库连接
	dbConn := sqlx.MustNewConn(c.DB)

	// 初始化 Redis 连接
	redisCli := redis.MustNewRedis(c.CacheRedis[0].RedisConf)

	// URL 校验配置:从 yaml 注入,业务侧只读这一份
	//   - Hosts: host 白名单(从 yaml 读)
	//   - Hosts 为空时用 bucket.endpoint 兜底
	hosts := append([]string{}, c.OSS.Hosts...)

	// OSS 客户端单例(只 New 一次, 内部 STS 凭证缓存才能生效)
	ossClient := oss.NewClient(oss.Config{
		Endpoint:        c.OSS.Endpoint,
		Bucket:          c.OSS.Bucket,
		Region:          c.OSS.Region,
		RoleArn:         c.OSS.RoleArn,
		AccessKeyID:     c.OSS.AccessKeyID,
		AccessKeySecret: c.OSS.AccessKeySecret,
		UploadHost:      c.OSS.UploadHost,
		URLPrefix:       c.OSS.URLPrefix,
		Hosts:           hosts,
	})

	return &ServiceContext{
		Config:        c,
		DBConn:        dbConn,
		RedisCli:      redisCli,
		TencentMapKey: c.TencentMap.Key,
		OSS: OSSBundle{
			Client: ossClient,
			HostCfg: oss.HostConfig{
				Hosts:    hosts,
				Bucket:   c.OSS.Bucket,
				Endpoint: c.OSS.Endpoint,
			},
		},

		// Model 注入：每个 model 内部自动用 c.CacheRedis 创建缓存连接
		UserModel:     model.NewUserModel(dbConn, c.CacheRedis),
		PostInfoModel: model.NewPostInfoModel(dbConn, c.CacheRedis),
		ShopModel:     model.NewShopModel(dbConn, c.CacheRedis),
		NoticeModel:   model.NewNoticeModel(dbConn, c.CacheRedis),
		BannerModel:   model.NewBannerModel(dbConn, c.CacheRedis),
		AdminModel:    model.NewAdminModel(dbConn, c.CacheRedis),

		// 评论 model 是 goctl 无 cache 生成,只接受 dbConn;不要传 c.CacheRedis
		//   - user_model / post_info_model 是带 cache 的(走 NewUserModel(dbConn, c.CacheRedis))
		//   - comment / comment_like / comment_report 不是 (走 NewCommentModel(dbConn))
		CommentModel:       model.NewCommentModel(dbConn),
		CommentLikeModel:   model.NewCommentLikeModel(dbConn),
		CommentReportModel: model.NewCommentReportModel(dbConn),
	}
}
