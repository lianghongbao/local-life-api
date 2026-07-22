// Code scaffolded by goctl. Safe to edit.
// goctl 1.10.1
//
// 与项目内 admin_*_handler.go 保持一致：
//   - 走 xhttp.JsonBaseResponseCtx 实现 {code, msg, data} 统一信封
//   - 错误也走同一个入口,前端 utils/request.ts 才能正确按 code 0/非 0 区分

package admin

import (
	"net/http"

	"github.com/zeromicro/go-zero/rest/httpx"
	xhttp "github.com/zeromicro/x/http"
	"local-life-api/internal/logic/admin"
	"local-life-api/internal/svc"
	"local-life-api/internal/types"
)

func AdminCommentFeedListHandler(svcCtx *svc.ServiceContext) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req types.AdminCommentFeedListReq
		if err := httpx.Parse(r, &req); err != nil {
			xhttp.JsonBaseResponseCtx(r.Context(), w, err)
			return
		}

		l := admin.NewAdminCommentFeedListLogic(r.Context(), svcCtx)
		resp, err := l.AdminCommentFeedList(&req)
		if err != nil {
			xhttp.JsonBaseResponseCtx(r.Context(), w, err)
		} else {
			xhttp.JsonBaseResponseCtx(r.Context(), w, resp)
		}
	}
}
