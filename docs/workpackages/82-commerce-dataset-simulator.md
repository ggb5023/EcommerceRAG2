# 82 拟真电商数据集 + 模拟后端

- 状态：见 INDEX.md
- 依据：决策 0041；用户要求真实商品/库存概念与可复用的模拟电商后端
- 前置：WP81 已关

## 目标

1. 本地可复现的 `commerce-lab-v1` 数据集（无外网抓取、无 PII、无金额汇总）；
2. Go `commerce-sim`：严格实现既有 facts HTTP 契约，并提供 dev REST 读写（工单内存态）；
3. 接入 dev supervisor、隔离栈、独立启停与一键演示；知识文档可导入发布。

## 步骤

1. `scripts/dev/generate_commerce_dataset.py`：固定 seed/参考日期，生成 17 个文件；
   `--check` 逐字节校验；`python/tests/test_commerce_dataset.py` 校验哈希、行数、
   引用完整性、无 PII、无金额汇总列；
2. `go/internal/commercesim`：加载数据集并校验每个文件 sha256；facts 五工具、
   版本/租户/店铺/资源绑定校验、故障注入；dev REST 订单/库存/物流/工单/healthz；
   `go/cmd/commerce-sim` 仅 loopback；
3. supervisor 增加可选 `commerce` 服务（数据集绝对路径 + loopback 地址，
   空 `V2_FACTS_BASE_URL` 时自动指向它）；
4. `isolated-stack.sh --commerce-sim`（`base+4`，metrics `base+5/6`）、
   `scripts/dev/commerce-sim.sh`、`scripts/dev/commerce-lab.sh`；
5. `demo-shop.sh --package` 支持任意知识包；数据集 manifest 含 5 份知识文档；
6. dev.env 指向 8091；`docs/development.md` 记录唯一用法。

## 验收命令

```bash
python3 scripts/dev/generate_commerce_dataset.py --check
cd python && uv run --frozen pytest tests/test_commerce_dataset.py -q
cd go && go test ./internal/commercesim/ -count=1
bash scripts/check.sh --smoke
```

## 限制 / 未运行

- dev/隔离栈是 mock 画像，模型不会自动请求 facts；真实售后事实链路留给 WP84/真实
  模型演示栈。本包用真实 `facts.HTTPProvider` 单测覆盖契约。
- 知识包已导入隔离栈测试库（5 份文档 active），dev 库未导入。

## 不要做

- 不改 `demo-store-v1` 与其哈希；不抓取外网；不把库存/价格写进 `catalog_sku`；
- 模拟器不得绑定非 loopback，不参与 selfhost。
