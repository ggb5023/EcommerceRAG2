#!/usr/bin/env bash
# 开发者环境引导：任意仓库路径、任意 Ubuntu 24.04 机器都能跑。
# 版本以 toolchains.lock.json 为准；能拿到官方校验和的下载都会校验。
# 用法：sudo bash scripts/dev/bootstrap-dev.sh
set -euo pipefail
[[ $EUID -eq 0 ]] || { echo "需要 root（安装系统包并写入 /usr/local）" >&2; exit 1; }
export DEBIAN_FRONTEND=noninteractive

repo=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd)
lock="$repo/toolchains.lock.json"
[[ -f "$lock" ]] || { echo "缺少 $lock" >&2; exit 1; }
lock_value() { python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))[sys.argv[2]])' "$lock" "$1"; }

GO_VER=$(lock_value go)
NODE_VER=$(lock_value node)
NODE_TAG="v$NODE_VER"
UV_VER=$(lock_value uv)
BUF_VER=$(lock_value buf)
GRPCURL_VER=$(lock_value grpcurl)

root=/opt/ecommerce-toolchains
cache="$root/downloads"
install -d "$root" "$cache" /usr/local/bin
install -d -m 700 /etc/ecommerce-rag

say() { printf '\n== %s ==\n' "$*"; }

# 依次尝试多个地址下载到同一个文件；成功后立刻写入目标路径。
download() {
  local dest=$1; shift
  [[ -s "$dest" ]] && return 0
  local url
  for url in "$@"; do
    if curl -fsSL --connect-timeout 10 --max-time 600 --retry 2 "$url" -o "$dest.partial"; then
      mv "$dest.partial" "$dest"
      return 0
    fi
    rm -f "$dest.partial"
  done
  return 1
}

verify_sha() { # verify_sha <file> <expected>
  local expected=${2:-}
  [[ -n "$expected" ]] || { echo "  ! 无校验和，跳过校验: $1"; return 0; }
  echo "$expected  $1" | sha256sum -c - >/dev/null
}

# go.dev/dl/?mode=json 只列当前补丁版本；历史版本用内置校验和兜底。
go_known_sha() {
  case "$1" in
    1.27.1) echo 63d339f0da5ab53635a56f2490a7984dfe12dfcff22ad749f63edaf590168445 ;;
    *) echo "" ;;
  esac
}

say "系统包"
apt-get update -qq
apt-get install -y -qq build-essential git curl ca-certificates jq unzip python3 python3-venv
for pkg in shellcheck protobuf-compiler postgresql-client-16 redis-tools; do
  apt-get install -y -qq "$pkg" || echo "  ! 跳过可选包 $pkg"
done

say "Docker"
if command -v docker >/dev/null; then
  docker --version
else
  bash "$repo/scripts/db/install-docker.sh"
fi

say "Go $GO_VER"
# 已有匹配版本就沿用，避免覆盖别的工具链（例如 dsh 自带的 node）依赖的链接
if command -v go >/dev/null && go version 2>/dev/null | grep -q "go$GO_VER "; then
  echo "  已存在 go$GO_VER（$(command -v go)），保持现有安装"
else
  if [[ ! -x "$root/go-$GO_VER/bin/go" ]]; then
    tarball="$cache/go$GO_VER.linux-amd64.tar.gz"
    download "$tarball" \
      "https://go.dev/dl/go$GO_VER.linux-amd64.tar.gz" \
      "https://mirrors.aliyun.com/golang/go$GO_VER.linux-amd64.tar.gz" || { echo "Go 下载失败" >&2; exit 1; }
    # 官方清单里有每个文件的 sha256；取不到就只告警，不阻塞。
    manifest="$cache/go-releases.json"
    if download "$manifest" "https://go.dev/dl/?mode=json"; then
      expected=$(python3 - "$manifest" "$GO_VER" <<'PY'
import json, sys
data = json.load(open(sys.argv[1]))
for release in data:
    if release.get("version") == "go" + sys.argv[2]:
        for f in release.get("files", []):
            if f.get("filename") == f"go{sys.argv[2]}.linux-amd64.tar.gz":
                print(f.get("sha256", ""))
                raise SystemExit
PY
)
      [[ -n "$expected" ]] || expected=$(go_known_sha "$GO_VER")
      verify_sha "$tarball" "$expected"
    else
      echo "  ! 取不到 Go 校验清单，跳过校验"
    fi
    install -d "$root/go-$GO_VER"
    tar -xzf "$tarball" --strip-components=1 -C "$root/go-$GO_VER"
  fi
  ln -sfn "$root/go-$GO_VER/bin/go" /usr/local/bin/go
  ln -sfn "$root/go-$GO_VER/bin/gofmt" /usr/local/bin/gofmt
fi

say "Node $NODE_VER"
if command -v node >/dev/null && [[ "$(node --version 2>/dev/null)" == "$NODE_TAG" ]]; then
  echo "  已存在 node $NODE_TAG（$(command -v node)），保持现有安装"
else
  if [[ ! -x "$root/node-$NODE_VER/bin/node" ]]; then
    sums="$cache/node-SHASUMS256.txt"
    download "$sums" \
      "https://nodejs.org/dist/$NODE_TAG/SHASUMS256.txt" \
      "https://npmmirror.com/mirrors/node/$NODE_TAG/SHASUMS256.txt" || { echo "Node 校验和下载失败" >&2; exit 1; }
    tarball="$cache/node-$NODE_TAG-linux-x64.tar.xz"
    download "$tarball" \
      "https://nodejs.org/dist/$NODE_TAG/node-$NODE_TAG-linux-x64.tar.xz" \
      "https://npmmirror.com/mirrors/node/$NODE_TAG/node-$NODE_TAG-linux-x64.tar.xz" || { echo "Node 下载失败" >&2; exit 1; }
    expected=$(awk -v f="node-$NODE_TAG-linux-x64.tar.xz" '$2 == f {print $1}' "$sums")
    verify_sha "$tarball" "$expected"
    install -d "$root/node-$NODE_VER"
    tar -xJf "$tarball" --strip-components=1 -C "$root/node-$NODE_VER"
  fi
  for binary in node npm npx; do ln -sfn "$root/node-$NODE_VER/bin/$binary" "/usr/local/bin/$binary"; done
fi

say "uv $UV_VER"
if ! command -v uv >/dev/null; then
  if download "$cache/uv.tar.gz" \
      "https://github.com/astral-sh/uv/releases/download/$UV_VER/uv-x86_64-unknown-linux-gnu.tar.gz"; then
    if download "$cache/uv.sha256" \
        "https://github.com/astral-sh/uv/releases/download/$UV_VER/uv-x86_64-unknown-linux-gnu.tar.gz.sha256"; then
      verify_sha "$cache/uv.tar.gz" "$(awk '{print $1}' "$cache/uv.sha256")"
    fi
    tar -xzf "$cache/uv.tar.gz" -C "$cache"
    install -m 755 "$cache/uv-x86_64-unknown-linux-gnu/uv" /usr/local/bin/uv
    install -m 755 "$cache/uv-x86_64-unknown-linux-gnu/uvx" /usr/local/bin/uvx
  else
    # GitHub 不可达时的兜底：官方安装脚本会挑选可用镜像。
    curl -fsSL "https://astral.sh/uv/$UV_VER/install.sh" | env UV_INSTALL_DIR=/usr/local/bin sh
  fi
fi
uv python install "$(lock_value python_managed)"

say "buf $BUF_VER"
if ! command -v buf >/dev/null; then
  if download "$cache/buf-Linux-x86_64" \
      "https://github.com/bufbuild/buf/releases/download/v$BUF_VER/buf-Linux-x86_64"; then
    if download "$cache/buf-sha256.txt" \
        "https://github.com/bufbuild/buf/releases/download/v$BUF_VER/sha256.txt"; then
      verify_sha "$cache/buf-Linux-x86_64" "$(awk '$2 == "buf-Linux-x86_64" {print $1}' "$cache/buf-sha256.txt")"
    fi
    install -m 755 "$cache/buf-Linux-x86_64" /usr/local/bin/buf
  else
    echo "  GitHub 不可达，改用 go install 构建 buf"
    GOBIN=/usr/local/bin GOFLAGS=-mod=mod go install "github.com/bufbuild/buf/cmd/buf@v$BUF_VER"
  fi
fi

say "grpcurl $GRPCURL_VER"
if ! command -v grpcurl >/dev/null; then
  asset="grpcurl_${GRPCURL_VER}_linux_x86_64.tar.gz"
  if download "$cache/$asset" \
      "https://github.com/fullstorydev/grpcurl/releases/download/v$GRPCURL_VER/$asset"; then
    if download "$cache/grpcurl-checksums.txt" \
        "https://github.com/fullstorydev/grpcurl/releases/download/v$GRPCURL_VER/grpcurl_${GRPCURL_VER}_checksums.txt"; then
      verify_sha "$cache/$asset" "$(awk -v f="$asset" '$2 == f {print $1}' "$cache/grpcurl-checksums.txt")"
    fi
    tar -xzf "$cache/$asset" -C "$cache" grpcurl
    install -m 755 "$cache/grpcurl" /usr/local/bin/grpcurl
  else
    echo "  GitHub 不可达，改用 go install 构建 grpcurl"
    GOBIN=/usr/local/bin GOFLAGS=-mod=mod go install "github.com/fullstorydev/grpcurl/cmd/grpcurl@v$GRPCURL_VER"
  fi
fi

say "校验版本（以 toolchains.lock.json 为准）"
python3 - "$GO_VER" "$NODE_VER" "$UV_VER" "$BUF_VER" "$GRPCURL_VER" <<'PY'
import shutil, subprocess, sys
go, node, uv, buf, grpcurl = sys.argv[1:6]
def run(cmd):
    try:
        # 有的工具把版本号打到 stderr（例如 grpcurl），两边都收
        done = subprocess.run(cmd, capture_output=True, text=True)
        return (done.stdout + done.stderr).strip()
    except FileNotFoundError:
        return ""
checks = [
    ("go", f"go version go{go} ", run(["go", "version"])),
    ("node", f"v{node}", run(["node", "--version"])),
    ("uv", f"uv {uv}", run(["uv", "--version"])),
    ("buf", buf, run(["buf", "--version"])),
    ("grpcurl", grpcurl, run(["grpcurl", "--version"])),
]
failed = 0
for name, expected, actual in checks:
    ok = expected in actual
    if not ok and name == "grpcurl" and "dev build" in actual:
        # 从源码 go install 构建时没有版本号，属于预期
        ok = True
    print(f"  {'OK  ' if ok else 'FAIL'} {name}: {actual or 'missing'}")
    failed += 0 if ok else 1
if not shutil.which("codex"):
    print("  -- codex 未安装（可选，不参与校验）")
raise SystemExit(1 if failed else 0)
PY

say "完成"
echo "仓库: $repo"
echo "接下来: (cd python && uv sync --frozen) / (cd frontend && npm ci --include=dev) / bash scripts/check.sh"
echo "运行时配置与 provider 凭据的写法见 docs/development.md#运行配置"
