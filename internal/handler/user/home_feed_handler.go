package user

import (
	"net/http"

	"github.com/zeromicro/go-zero/rest/httpx"
	xhttp "github.com/zeromicro/x/http"
	"local-life-api/internal/logic/user"
	"local-life-api/internal/svc"
	"local-life-api/internal/types"
)

// HomeFeedHandler 首页本地推荐混合流（C 端公开接口，无需登录）
//
// 走 xhttp.JsonBaseResponseCtx 包统一信封（与 HomeIndex / PostList 等保持一致），
// 避免 httpx.ErrorCtx / httpx.OkJsonCtx 返回非 envelope 形态。
func HomeFeedHandler(svcCtx *svc.ServiceContext) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req types.HomeFeedRequest
		if err := httpx.Parse(r, &req); err != nil {
			xhttp.JsonBaseResponseCtx(r.Context(), w, err)
			return
		}

		l := user.NewHomeFeedLogic(r.Context(), svcCtx)
		resp, err := l.HomeFeed(&req)
		if err != nil {
			xhttp.JsonBaseResponseCtx(r.Context(), w, err)
		} else {
			xhttp.JsonBaseResponseCtx(r.Context(), w, resp)
		}
	}
}