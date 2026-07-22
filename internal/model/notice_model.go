package model

import (
	"github.com/zeromicro/go-zero/core/stores/cache"
	"github.com/zeromicro/go-zero/core/stores/sqlx"
)

var _ NoticeModel = (*customNoticeModel)(nil)

type (
	// NoticeModel is an interface to be customized, add more methods here,
	// and implement the added methods in customNoticeModel.
	NoticeModel interface {
		noticeModel
	}

	customNoticeModel struct {
		*defaultNoticeModel
	}
)

// NewNoticeModel returns a model for the database table.
func NewNoticeModel(conn sqlx.SqlConn, c cache.CacheConf, opts ...cache.Option) NoticeModel {
	return &customNoticeModel{
		defaultNoticeModel: newNoticeModel(conn, c, opts...),
	}
}
