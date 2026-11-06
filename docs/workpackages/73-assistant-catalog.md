# 73 AI 查询台与商品目录浏览

- 状态：见 INDEX.md
- 依据：用户决定保留独立 AI 查询台（知识问答 + 商品目录），不做实时库存
- 前置：WP72 已关

## 目标

商家与员工共用的 `/assistant`：知识问答（复用现有 RAG 工作台）+ `/assistant/catalog`
只读商品目录（catalog_sku，已发布/有效/当前版本）。不展示任何货币金额（0021）。

## 后端

1. `GET /v1/catalog/skus`（gateway，读角色 owner/admin/operator/viewer）：
   - `shop_id` 默认会话店铺，必须在 AllowedShops；
   - 业务日期用 `auth.ShopBusinessDate`（店铺时区），不用浏览器时间；
   - 仅返回 `version.status='active'`、`document.active_version_id=v.id`、
     `review_status='approved'`、`document.status='active'`、有效期覆盖业务日期的行；
   - 筛选：q（标题/SKU/品牌）、category、sku、attribute_key+attribute_value（属性白名单
     `domain.CatalogAttributeAllowed`）、page/page_size（1..100，默认 24）；
   - 返回 SKU/SPU/标题/品类/品牌/颜色/尺码/尺码表/attributes/shop_id + total + notice；
     **不返回价格、库存、订单字段**；
   - 错误：`invalid_catalog_query` 400、`role_forbidden`/`admin_scope_denied` 403、
     `catalog_unavailable` 503。
2. SQL 放 `go/internal/gateway/store_catalog.go`（`catalogBrowseQuery` 可单测）；
   handler `catalog_browser.go`；路由注册 `routes_customer.go`。
3. openapi/error-taxonomy 同步；Go 单测覆盖参数解析、可见性条件、禁止商业列。

## 前端

1. 路由：`/assistant`（App.vue，品牌改「AI 查询台」）、`/assistant/catalog`
   （AssistantCatalog.vue）；`/chat`、`/chat/:id` 重定向到 `/assistant`；
   登录回跳白名单加 `/assistant`、`/assistant/catalog`。
2. `AssistantCatalog.vue`：筛选（关键词/品类/SKU/属性名值）、表格、分页、
   加载/错误/空态、服务端 notice；样式只用 tokens。
3. `catalog-api.ts`：`searchCatalog`（查询串、403 映射为可读错误）。
4. App.vue 头部加「商品目录」链接；工作台内会话跳转改 `/assistant`。

## 验收命令

```bash
cd go && go build ./... && go vet ./... && go test ./internal/gateway/ -run "Catalog"
cd frontend && npm test -- --run && npm run lint && npm run build
bash scripts/check.sh
# 真实链路（dev，店铺 staff）：登录后 /v1/catalog/skus 200；非法属性 400
```

## 不要做

- 不展示价格/货币金额（0021）；不做实时库存/订单；
- 不加依赖、不新增迁移、不改 RAG 路径；
- 不改 smoke 基线/棘轮。

## 汇报

五节格式；附端点响应、非法参数 400、前端测试输出；说明 dev 目录为空的原因
（仅有 revoked 商品版本），正例由查询契约测试与演示栈覆盖。
