#!/bin/bash
# GSIC Windows 版交叉编译（Linux 上编 Windows exe + NSIS 安装包）。
# 版本号从 main.go 的 Version / VersionCode 读取，与源码保持一致。
#
# 依赖：Go 1.25+、Python 3；安装包还需要 makensis（NSIS 3）。
# rsrc 缺失时自动 go install；图标缺失时自动从 base64 内嵌文件还原。
set -euo pipefail
cd "$(dirname "$0")"

VERSION=$(grep -oP 'Version\s*=\s*"\K[^"]+' main.go)
VERSION_CODE=$(grep -oP 'VersionCode\s*=\s*\K[0-9]+' main.go)
echo "构建 GSIC Windows v$VERSION ($VERSION_CODE)"

# 1. 还原图标（.ico 由 base64 内嵌文件生成，构建产物不进版本跟踪）
if [ ! -f assets/gsic.ico ]; then
  python3 assets/restore_icon.py
fi

# 2. 生成资源文件（应用清单 + 图标）。
#    清单是必需的：缺 Common-Controls v6 清单时 Walk 界面创建窗口会失败。
RSRC=${RSRC:-$(command -v rsrc || true)}
if [ -z "$RSRC" ]; then
  echo "rsrc 未找到，正在安装 github.com/akavel/rsrc ..."
  go install github.com/akavel/rsrc@latest
  RSRC="$(go env GOPATH)/bin/rsrc"
fi
"$RSRC" -manifest gsic.manifest -ico assets/gsic.ico -o gsic_windows_amd64.syso

# 3. 交叉编译（windowsgui：无控制台窗口）
GOOS=windows GOARCH=amd64 go build -ldflags "-H windowsgui -s -w" \
  -o "gsic-$VERSION-windows-x64.exe" .

# 4. NSIS 安装包（Linux 下 makensis 传参用 -D，不要用 /D）
if command -v makensis >/dev/null 2>&1; then
  cp "gsic-$VERSION-windows-x64.exe" installer/gsic.exe
  cp assets/gsic.ico installer/gsic.ico
  (cd installer && makensis -DVERSION="$VERSION" -DVERSION_CODE="$VERSION_CODE" \
    -DOUTFILE="../GSIC-Setup-$VERSION.exe" gsic.nsi)
  rm -f installer/gsic.exe
  echo "安装包：GSIC-Setup-$VERSION.exe"
else
  echo "makensis 未找到，跳过安装包（只产出 exe）"
fi

SHA256=$(sha256sum "gsic-$VERSION-windows-x64.exe" | awk '{print $1}')
echo "sha256: $SHA256"
# version-windows.json：自更新源文件。Issue #4 起更新强制要求有效 hash，
# 此处直接生成，发版时把它作为 Release 附件上传即可（不要手写）。
python3 - "$VERSION" "$VERSION_CODE" "$SHA256" <<'EOF'
import json, sys
version, code, sha = sys.argv[1], int(sys.argv[2]), sys.argv[3]
info = {
    "version": version,
    "versionCode": code,
    "exeUrl": "https://github.com/Jinzhiying2004/GSIC-AGENT/releases/download/v%s/gsic-%s-windows-x64.exe" % (version, version),
    "sha256": sha,
    "notes": "",
}
with open("version-windows.json", "w", encoding="utf-8") as f:
    json.dump(info, f, indent=2)
    f.write("\n")
print("已生成 version-windows.json（发版时作为 Release 附件上传）")
EOF
python3 -m json.tool version-windows.json > /dev/null && echo "version-windows.json 校验通过"
