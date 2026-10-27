# 工程治理与供应链（重构方案 · 治理层）

版本：v1　日期：2026-10-27　适用仓库：`/data/app/EcommerceRAG2`
角色：补充总纲未覆盖的**流程与依赖**维度：分支、里程碑、决策编号、依赖升级、文档防漂移。

---

## 0. 现状（2026-10-27 实测）

| 项 | 现状 |
| --- | --- |
| 分支 | **11 条**：`main` + m2/m3/m4-final/m4-local-accounts/m5-deploy/m6/m7/m8 + `eval-tooling`/`oss-backup`（后两条停在旧提交） |
| 已合并未删 | m2–m7 全部已合入 main，但分支都还在 |
| 里程碑 tag | **无**（只有 `baseline-20261016`） |
| 决策记录 | 21 份；编号曾出现计划冲突（M9 想用 0021，已被占用） |
| 门禁 | 仅本地 `bash scripts/check.sh`（实测 31.1s），**无 CI** |
| 依赖 | Python 直接依赖 10 个、前端 9 个；锁文件 3 个（`go.sum`/`uv.lock`/`package-lock.json`）；5 个镜像中 3 个 pin 了 digest |
| 依赖扫描 | 无（无 govulncheck / pip-audit / npm audit 接入） |
| 文档漂移 | 0021 落地后仍有文档写"预算账本"；现役脚本已是 `scripts/dev/provider-usage.py`，清理范围见总纲 R0 |

---

## 1. 问题清单

### G1 分支即合即删没有执行

12 条分支里 7 条已合并未删、2 条停止维护，`git branch` 已经不能用来判断"现在在做什么"。这正是 M1 想根治的问题在 M8 期间复现。

### G2 决策编号会撞车

M9 文档原本要写 `0021-frontend-design-system.md`，而 0021 已被 usage-log 占用。编号靠"看一眼目录"分配，在并行开发时必然出错。

### G3 没有里程碑 tag，回滚点不明确

m6/m7/m8 合并后没有 tag；想知道"L2 评审上线时的代码"只能翻 merge commit。

### G4 依赖与漏洞无节奏

三个生态都没有定期升级与漏洞扫描；`oss2` 这类可选依赖装了不装全靠运气。

### G5 文档会漂移，且没有机械检查

0021 改了口径，7 个文件没跟上——这不是第一次，也不会是最后一次。

---

## 2. 工作包

### G-W1　分支治理（10 分钟，每次合并后执行）

```
规则：分支数上限 3（main + 当前里程碑 + 紧急修复）
合并后 24h 内：git push origin main && git branch -d <分支> && git push origin --delete <分支>
需要留档：git tag milestone/m<N> <merge-commit>
```

提供 `scripts/dev/git-prune.sh`（只列出"已合并可删"的分支，不自动删）：

```bash
for b in $(git branch --format='%(refname:short)'); do
  [ "$b" = main ] && continue
  git merge-base --is-ancestor "$b" main && echo "可删: $b"
done
```

验收：`git branch | wc -l` ≤ 3；`git tag | grep milestone` 有 m6/m7/m8。

**已归档分支**（标签留档，不再复活）：

- `archive/m4-local-accounts`：三条提交的补丁等价物已在 main（`71dba14` / `f265d5f` / `5dff9e2`），
  分支放弃，标签留档。

### G-W2　决策编号分配表（2 小时）

在 `docs/decisions/0003-documentation-system.md` 的"决策一览"里维护一张**预留表**：

```
已用：0001–0021
预留：0022 数据归属 / 0023 密钥生命周期 / 0024 前端设计系统 / 0025 顾客数据保留
规则：新增前先在表里登记（同一提交），再写文件；同号存在则追加补充，不另建
```

验收：`ls docs/decisions | wc -l` 与表内编号数一致；无重复编号。

### G-W3　里程碑 tag 与发布说明（每次合并）

```
git tag -a milestone/m<N> <merge-commit> -m "M<N> <主题>"
git push origin milestone/m<N>
```

`docs/install.md` 的升级章节引用最近三个 milestone tag 作为"可回滚点"。

### G-W4　依赖与漏洞扫描（半天，接入 `check.sh --deps` 层）

```
go:      go list -m -u all | head -50 ; govulncheck ./...
python:  uv pip list --outdated ; pip-audit（或 uvx pip-audit）
前端:    npm outdated ; npm audit --omit=dev
镜像:    每月检查 3 个 digest 是否有新版本
```

规则：**升级单独提交**（`chore(deps): …`），不与功能混合；漏洞扫描作为**非阻塞层**，结果写 `.local/deps/`。

验收：`bash scripts/check.sh --deps` 产出三个文件（go/python/frontend），exit 0。

### G-W5　文档防漂移检查（2 小时，接入快速层）

`scripts/check/check-docs-drift.py`：

- 禁止在 `docs/**`（除 `docs/decisions/**`）出现已废弃配置键：`V2_ONLINE_BUDGET`、`provider-budget`、`预算账本`
- **白名单用同构正则**，与 `.gitignore` 的 `!/docs/*-plan.md` 等白名单保持同一集合：
  ```python
  ALLOW = re.compile(r'^docs/(?:[a-z0-9-]+-plan|refactor-assessment|refactor-input)\.md$')
  ```
  这些文件本身就在讨论废弃键，必须排除，否则重构文档会把自己判成违规
- 禁止文档里引用的 `scripts/...` 路径不存在
- 禁止文档里出现的 `docs/decisions/NNNN-*.md` 链接指向不存在的文件

验收：`python3 scripts/check/check-docs-drift.py` → `PASS`；故意在 `docs/install.md` 里加一行 `V2_ONLINE_BUDGET_CNY` 应 fail；
在 `docs/governance-plan.md`、`docs/refactor-assessment.md`、`docs/testing-refactor-plan.md` 里出现同样的词应 **不** fail（同构白名单生效）。

---

## 3. 红线

1. 不使用 `git add .`、`--no-verify`、force push
2. 合并后必须推送 origin，本地不积压超过一个里程碑
3. 依赖升级必须单独提交，且附 `check.sh` 输出
4. 决策编号先在索引里登记再写文件

## 4. 验收总表

| 检查 | 命令 | 期望 |
| --- | --- | --- |
| 分支数 | `git branch | wc -l` | ≤ 3 |
| 里程碑 tag | `git tag | grep -c milestone` | ≥ 3 |
| 决策编号 | 索引表 vs 目录 | 一一对应 |
| 依赖扫描 | `bash scripts/check.sh --deps` | exit 0，三份产物 |
| 文档漂移 | `python3 scripts/check/check-docs-drift.py` | PASS |
