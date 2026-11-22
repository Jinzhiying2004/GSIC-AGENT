# GSIC Windows v4.32 测试报告

测试时间：2026-11-21 22:00（Asia/Shanghai）
测试环境：Linux x64 云端虚拟机（无 Windows 真机）
构建工具：Go 1.23.4，golang.org/x/sys v0.28.0

## 1. 编译结果

| 目标 | 结果 |
|---|---|
| `gofmt` + `go vet ./...` | 通过，无警告 |
| Windows 交叉编译 `GOOS=windows GOARCH=amd64` | 成功 |
| 输出 | `gsic-4.32-windows-x64.exe`，5,989,376 字节 |
| 文件类型 | PE32+ executable (console) x86-64, for MS Windows |
| MD5 | 4f0c26ad29355633668c0fc91acfdabd |
| SHA256 | f5174d6ea9dbaeb87b01fec29b9fed4c211b9509ba14ff00b9837943d19996dc |
| strings 检查 | 含 SendInput / BitBlt / GetDIBits / SetForegroundWindow / OpenClipboard / user32.dll / gdi32.dll / 版本 4.32 / 默认端口 28789 |
| 代码检查 | 无全角括号【】；缩进完整（gofmt 校验通过） |

## 2. 冒烟测试（Linux 构建，同源代码，Windows API 为桩实现）

Linux 构建用于验证 HTTP 服务、鉴权、任务执行器、文件/命令/技能卡等
平台无关逻辑。Windows 专属调用（鼠标/键盘/截图/窗口/剪贴板）在 Linux
上返回明确的 "not supported on this platform (Windows-only feature)"
错误，不崩溃、不挂起。

| # | 测试 | 结果 |
|---|---|---|
| 1 | GET / 无需鉴权，返回名字+版本 | 通过 |
| 2 | GET /status 无 token -> 401 | 通过 |
| 3 | GET /status 带正确 token -> 完整状态 JSON | 通过 |
| 4 | GET /status 错误 token -> 401 | 通过 |
| 5 | /task：shell 回显 + file_write 中文 + file_read 回环 + dir_list + sleep | 通过（中文 "中文测试 hello" 18 字节回环一致） |
| 6 | /task：screenshot 在 Linux 上优雅失败且 stop_on_error 停止后续步骤 | 通过 |
| 7 | POST /shell 直接执行命令 | 通过 |
| 8 | GET /dir 列目录（目录优先、按名排序） | 通过 |
| 9 | POST /cards 创建技能卡 | 通过 |
| 10 | GET /cards 列出技能卡 | 通过 |
| 11 | /task use_card 展开执行卡内步骤（裸数组 body 亦支持），结果带 card_id | 通过 |
| 12 | 未知 action / 未知 card_id 返回明确错误 | 通过 |
| 13 | /clipboard、/windows 在 Linux 上优雅报错 | 通过 |
| 14 | DELETE /card 删除后再次查询返回 unknown card | 通过 |
| 15 | stop_on_error=false 时失败后继续执行后续步骤 | 通过 |
| 16 | 无 token 启动时自动生成 64 位十六进制 token 并写入 config.json | 通过 |
| 17 | `-version` 输出 `GSIC 4.32 (build 432)` | 通过 |

结论：17/17 通过。平台无关逻辑全部验证通过。

## 3. 待 Windows 真机验证（云端无法验证，必须在 Windows 10/11 上跑）

| # | 验证项 | 方法 |
|---|---|---|
| W1 | exe 双击启动，/status 返回真实屏幕尺寸 | 浏览器/PowerShell 调 /status |
| W2 | 截图返回真实桌面 JPEG（/screenshot，raw 与 base64 两种） | 调接口后打开图片核对 |
| W3 | 鼠标点击/拖拽/滚轮真实生效 | /task 发 click 到记事本图标等可见目标 |
| W4 | 键盘 type 中文 + 组合键 Ctrl+C 真实生效 | 记事本输入中文、复制粘贴验证 |
| W5 | 窗口列出与激活（/windows、/window/activate） | 打开两个窗口互相切换 |
| W6 | 剪贴板读写回环 | clipboard_set 后 clipboard_get |
| W7 | cmd 与 PowerShell 命令执行（中文输出） | /shell 发 `chcp` / `Get-Date` 等 |
| W8 | 技能卡 use_card 在 Windows 端执行 | 导入 examples/skill_card_example.json 后执行 |
| W9 | Tailscale 远控链路（Muse 云端经 tailnet 访问 127.0.0.1:28789） | 用户 PC 装 Tailscale 后联调 |
| W10 | 开机自启（启动文件夹/计划任务） | 重启后检查服务是否在跑 |

## 4. 已知限制

- 见 README.md"已知限制"一节（多屏截图为整张大图、前台窗口抢焦点限制、
  锁屏下操作无效、UIPI 管理员窗口隔离、无托盘图标、无自动更新）。
- Linux 冒烟构建的 shell 走 /bin/sh，仅用于测试；Windows exe 走 cmd/powershell。

## 5. 交付清单

- 源码：`~/workspace/gsic-windows/`（12 个 Go 文件 + go.mod/go.sum + README + 示例）
- 可执行文件：`~/workspace/gsic-4.32-windows-x64.exe`
- 未推 GitHub（按任务要求，等用户拍板）
