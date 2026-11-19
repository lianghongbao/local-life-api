# internal/model

goctl 生成的 model 层。**分两半**：

| 文件 | 谁生成 | 能不能改 |
|---|---|---|
| `<x>_model_gen.go` | goctl（`goctl model ...`） | **能，但会被重新生成覆盖** |
| `<x>_model.go` | 手写 | 能 |

## 重新生成 model 后要补的补丁

`<x>_model_gen.go` 里的一批 `cache*Prefix` 常量**被手工改过** —— 值前面加了
`local-life:` 应用前缀：

```go
cacheUserIdPrefix = "local-life:cache:user:id:"
```

为什么必须改这里：go-zero 的 `cache.Option` 不支持 key 前缀（只有 `WithExpiry` /
`WithNotFoundExpiry`），`cacheNode` 把 key 原样透传给 Redis；而且 key 是在生成代码里
内联 `fmt.Sprintf("%s%v", cacheUserIdPrefix, id)` 拼的，**没有拦截点**。
前缀只能落在 const 的值上。

共享底座的 Redis 上还有 chatroom 在跑（它的前缀是 `chatroom:`），不加前缀就会撞 key。

**`goctl model` 重新生成后，前缀会被打回原样** —— 要重新把
`"cache:` 改成 `"local-life:cache:`。本目录下的 8 个 const：

```
internal/model/admin_model_gen.go:27,28      cacheAdminIdPrefix / cacheAdminUsernamePrefix
internal/model/banner_model_gen.go:27       cacheBannerIdPrefix
internal/model/notice_model_gen.go:27       cacheNoticeIdPrefix
internal/model/post_info_model_gen.go:27    cachePostInfoIdPrefix
internal/model/shop_model_gen.go:27         cacheShopIdPrefix
internal/model/user_model_gen.go:27,28      cacheUserIdPrefix / cacheUserOpenidPrefix
```

前缀常量本身定义在 `internal/keys` 包 —— 那才是权威来源。

## 其它 model 层的 Redis key

`internal/model` 之外还有几处 key，都不在本目录：

- `internal/cachex/feed_version.go` —— 首页 feed version
- `internal/logic/user/home_feed_logic.go`、`home_index_logic.go` —— 首页缓存
- `internal/logic/user/geo_*_logic.go` —— 腾讯地图缓存
- `internal/background/view_count_flusher.go` —— 浏览数累加
- `internal/logic/user/comment_create_logic.go` —— 评论后删缓存

新增 key 时统一走 `internal/keys` 包，别在各处散着写字面量。
