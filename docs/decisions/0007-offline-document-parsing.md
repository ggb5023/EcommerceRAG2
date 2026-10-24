# 0007 文档解析保持离线，不接入默认摄取链路

日期：2026-10-24　状态：已采纳

## 背景

`python/app/providers/mineru.py` 是一个完整的 MinerU v4 客户端（提交、轮询、下载、解包、元素归一化），但 [产品定位](../product.md) 把「PDF 解析」列为首版不做，[路线图](../../ROADMAP.md) 的暂缓清单也包含 PDF。代码里存在该客户端，容易被误读为「PDF 已经在范围内」。

## 决定

- MinerU 只作为**离线解析实验**的能力保留在 `app/providers/`，不接入 `app/ingest` 的默认链条：导入资料的格式仍限定 `markdown`、`csv`、`docx`。
- 使用它必须由操作者显式触发，并在 `providers.env` 里提供 `MINERU_TOKEN`；缺少 token 时按配置错误处理，不自动降级。
- 摄取 worker 不读取 MinerU 配置，也不会因为 PDF 而改变任务状态。

## 后果

- 需要处理 PDF 时，先写新的决策记录说明为什么进入范围、由哪条链路调用、失败如何回滚，再改 `pipeline` 与格式白名单。
- 该客户端的行为仍受 `providers.env` 的 `0600` 权限与既有超时、大小上限约束，不新开旁路。
