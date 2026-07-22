package model

import "github.com/zeromicro/go-zero/core/stores/sqlx"

var _ CommentReportModel = (*customCommentReportModel)(nil)

type (
	// CommentReportModel is an interface to be customized, add more methods here,
	// and implement the added methods in customCommentReportModel.
	CommentReportModel interface {
		commentReportModel
		withSession(session sqlx.Session) CommentReportModel
	}

	customCommentReportModel struct {
		*defaultCommentReportModel
	}
)

// NewCommentReportModel returns a model for the database table.
func NewCommentReportModel(conn sqlx.SqlConn) CommentReportModel {
	return &customCommentReportModel{
		defaultCommentReportModel: newCommentReportModel(conn),
	}
}

func (m *customCommentReportModel) withSession(session sqlx.Session) CommentReportModel {
	return NewCommentReportModel(sqlx.NewSqlConnFromSession(session))
}
