package admin

import (
	"net/http"

	"github.com/zeromicro/go-zero/rest/httpx"
	xhttp "github.com/zeromicro/x/http"
	"local-life-api/internal/logic/admin"
	"local-life-api/internal/svc"
	"local-life-api/internal/types"
)

func AdminAccountChangePasswordHandler(svcCtx *svc.ServiceContext) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req types.AdminAccountChangePasswordReq
		if err := httpx.Parse(r, &req); err != nil {
			xhttp.JsonBaseResponseCtx(r.Context(), w, err)
			return
		}

		l := admin.NewAdminAccountChangePasswordLogic(r.Context(), svcCtx)
		resp, err := l.AdminAccountChangePassword(&req)
		if err != nil {
			xhttp.JsonBaseResponseCtx(r.Context(), w, err)
		} else {
			xhttp.JsonBaseResponseCtx(r.Context(), w, resp)
		}
	}
}
