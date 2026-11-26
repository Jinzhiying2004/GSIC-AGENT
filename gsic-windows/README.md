# GSIC Windows 版 v4.43

GSIC Windows 版是 Muse 的"手"，运行在 Windows 10/11 x64 PC 上。
它只执行、不决策：云端 Muse 大脑发步骤过来，它负责操作这台电脑
（鼠标、键盘、截图、命令、窗口、文件、剪贴板、技能卡）。

与 Android 版 GSIC 平行、协议一致：同一个 `/task` 步骤格式，
同一套 `X-Token` 鉴权，同一个默认端口 `28789`。

- 单文件 exe，无运行时依赖（Go 编写）
- 内置 Tailscale 节点（tsnet），自动加入 tailnet，远程访问无需另装 Tailscale App
- 原生 Windows 图形界面（Walk）：系统托盘图标 + 主窗口
- 安装包（NSIS）按用户安装，无需管理员权限；开机自启、桌面快捷方式一次到位
- 自更新：启动时 24 小时节流检查，支持 Muse 推送自动安装
- 监听 `127.0.0.1:28789`（本机）+ tailnet IP:28789（远程）；局域网默认不放行（`allow_lan` 可开，见下）
- 日志：`%APPDATA%\GSIC\gsic.log`（GUI 日志页可看最近 100 行）
- 数据目录：`%APPDATA%\GSIC\`（config.json、skill_cards.json、gsic.log、tailscale/）

## 快速开始

1. 从 [Releases](../releases) 下载 `GSIC-Setup-x.xx.exe`，双击安装。
   安装到 `%LOCALAPPDATA%\Programs\GSIC`（无需管理员权限；自更新要原地替换
   exe，必须装在用户可写目录，Program Files 会破坏更新）。
   首次运行自动生成 token，保存在 `%APPDATA%\GSIC\config.json`。
   界面「连接智能体」页会显示 Tailscale 登录链接，浏览器打开授权后，
   本机以 `gsic-win` 为名加入 tailnet，获得 100.x IP。
   托盘会出现 GSIC 图标，右键可显示主界面、检查更新、退出。
2. 验证服务（PowerShell）：

```powershell
$token = (Get-Content "$env:APPDATA\GSIC\config.json" | ConvertFrom-Json).token
Invoke-RestMethod -Uri http://127.0.0.1:28789/status -Headers @{"X-Token"=$token}
```

3. 发一个任务（打开记事本并输入文字）：

```powershell
$body = @{
  steps = @(
    @{ action="launch"; path="notepad.exe" },
    @{ action="sleep"; ms=800 },
    @{ action="type"; text="Hello from GSIC Windows" }
  )
} | ConvertTo-Json -Depth 5
Invoke-RestMethod -Uri http://127.0.0.1:28789/task -Method Post `
  -Headers @{"X-Token"=$token; "Content-Type"="application/json"} -Body $body
```

## 连接你的智能体（只需一次）

装好、登上 Tailscale 后，打开主窗口「连接智能体」页：

- **复制完整提示词**：一键复制提示词包（连接信息 + 全部接口说明 + 操作规矩），
  粘贴给你的智能体，它拿到就能直接干活
- **二维码**：把二维码拍给智能体识别，里面是地址 + token，
  并告诉智能体调 `GET /agent-prompt` 取完整提示词全文
- **复制地址和 Token**：只复制地址和 token 两项

二维码和提示词里都含 token，不要发给别人。

## 命令行参数

| 参数 | 说明 |
|---|---|
| `-port 28790` | 监听端口（默认 28789；与 Android 版同机时建议改 28790） |
| `-token <token>` | 指定 token（覆盖配置文件；不建议写进快捷方式） |
| `-data-dir <dir>` | 数据目录（默认 `%APPDATA%\GSIC`） |
| `-gen-token` | 生成新 token 并保存后退出 |
| `-no-log-file` | 只输出到控制台，不写日志文件 |
| `-no-tsnet` | 禁用内置 Tailscale 节点（远程访问随之不可用，除非开 `allow_lan`） |
| `-allow-lan` | 同时监听所有网卡（局域网可达，默认不开；见「远程访问」） |
| `-hostname <name>` | tailnet 节点名（默认 gsic-win） |
| `-no-auto-update` | 禁用自动更新检查 |
| `-no-gui` | 无界面运行（无托盘图标，纯控制台） |
| `-version` | 打印版本后退出 |

环境变量：`GSIC_PORT`、`GSIC_TOKEN`、`GSIC_DATA_DIR`。
优先级：命令行参数 > 环境变量 > config.json > 自动生成。

## 开机自启

安装包默认写入注册表 Run 键，开机自动启动；设置页可开关。
卸载时会移除 Run 键，但保留 `%APPDATA%\GSIC`（token 与 Tailscale 登录还在，
重装后无需重新配对）。

## 远程访问（内置 Tailscale）

v4.33 起内置 Tailscale 节点（tsnet），无需另装 Tailscale App：
- 首次运行在界面显示登录链接，浏览器打开授权后加入 tailnet
- **v4.42 起可用 Auth Key 加入（不用浏览器登录）**：机器上不了外网、登录不了谷歌时，在 Tailscale 管理控制台（login.tailscale.com）→ 设置 → 密钥 生成 Auth Key，粘贴到主窗口「状态」页 Tailscale 区的 Auth Key 输入框，点「用 Key 加入」；或远程调 `POST /tailscale/authkey {"auth_key": "tskey-auth-..."}`（需 X-Token）。Key 存 config.json（`tailscale_auth_key`，0600），只用于首次加入，已入网的节点忽略它；Key 无效时日志会明确提示。环境变量 `GSIC_TAILSCALE_AUTHKEY` 也可传 Key
- 节点名默认 `gsic-win`，可通过 `-hostname` 修改
- 节点状态存在 `%APPDATA%\GSIC\tailscale\`，重启后无需重新登录
- HTTP 服务只监听本机回环（127.0.0.1:28789）和 tailnet IP:28789；同一 Wi-Fi 的其他设备默认连不上（与根 README 的安全说明一致）
- 局域网访问是明确的显式开关：config.json 中 `allow_lan: true`，或加 `-allow-lan` 参数，才会监听 0.0.0.0；此时 token 以明文 HTTP 走局域网，只在受信任的内网开
- `/status` 返回 tsnet 状态（tsnet_ip、tsnet_running、tsnet_state）
- 如 PC 上已有外部 Tailscale App 在运行，内置节点与之共存（不同节点名）
- 禁用内置节点：加 `-no-tsnet` 参数（此时远程访问不可用，除非同时开 `allow_lan`）

不要用端口转发把 28789 暴露到公网；远程访问请走 tailnet。

## HTTP 接口

除 `/` 外所有接口都需要请求头 `X-Token: <token>`，否则返回 401。

| 接口 | 方法 | 说明 |
|---|---|---|
| `/` | GET | 存活探测，无需鉴权，只返回名字和版本 |
| `/status` | GET | 版本、平台、运行时间、屏幕尺寸、鼠标位置、前台窗口、技能卡数量 |
| `/task` | POST | 执行步骤数组，见下 |
| `/screenshot` | GET | 截图；`?quality=80`（1-100），`?raw=1` 直接返回 JPEG |
| `/windows` | GET | 列出可见顶层窗口（标题、句柄、PID、位置） |
| `/window/activate` | POST | `{"hwnd":123}` 或 `{"title":"记事本"}` 激活窗口 |
| `/clipboard` | GET/POST | 读/写剪贴板文本（POST body `{"text":"..."}`） |
| `/file` | GET/POST | 读/写文件（GET `?path=...&base64=1`；POST `{"path","content"}` 或 `{"path","base64"}`） |
| `/dir` | GET | 列目录（`?path=...`） |
| `/shell` | POST | 执行命令 `{"command":"dir","shell":"cmd","timeout":60}` |
| `/cards` | GET/POST | 列出/新增（或覆盖）技能卡 |
| `/card?id=...` | GET/DELETE | 取单个技能卡 / 删除技能卡 |
| `/agent-prompt` | GET | 返回填好本机连接信息的智能体提示词全文（JSON） |
| `/tailscale/authkey` | POST | 用 Auth Key 加入 tailnet（body `{"auth_key":"tskey-auth-..."}`，不用浏览器登录） |
| `/update/check` | GET/POST | 手动检查更新 |
| `/update/apply` | POST | 下载并应用最新版本（自动重启） |
| `/update/push` | POST | Muse 推送更新（body `{"version","url","sha256"}`，自动安装开关开时立即安装） |

## /task 步骤格式

POST `/task`，body 为 `{"steps": [...]}` 或裸数组 `[...]`。
默认第一步失败即停止（`stop_on_error`，设为 `false` 可继续执行后续步骤）。
多个任务串行执行（两个任务同时抢一个鼠标会互相干扰）。

返回：`{"ok": true, "task_id": "task-...", "results": [...]}`，
每个 result 含 `index`、`action`、`ok`、`error`（失败时）、`data`。

### 动作一览

| action | 参数 | 说明 |
|---|---|---|
| `click` / `tap` | `x, y, button="left", clicks=1` | 移到坐标并点击；button 可为 left/right/middle |
| `move` | `x, y` | 只移动鼠标 |
| `drag` | `x1,y1,x2,y2` 或 `from:{x,y}, to:{x,y}` | 左键拖拽 |
| `scroll` / `wheel` | `x, y, delta` | 滚轮；delta>0 上滚；绝对值小于 10 时按"格"计（x120） |
| `type` | `text` | 输入 Unicode 文本，支持中文 |
| `key` | `keys=["ctrl","c"]` 或 `"ctrl+c"` 或 `key="enter"` | 组合键/单键；支持 ctrl/alt/shift/win/enter/tab/esc/space/方向键/F1-F12 等 |
| `shell` | `command, shell="cmd"/"powershell"/"direct", timeout=60` | 执行命令，返回 stdout/stderr/exit_code |
| `screenshot` | `quality=80` | 截图，data.image 为 base64 JPEG |
| `sleep` / `wait` | `ms` 或 `seconds` | 等待（最长 5 分钟） |
| `window_list` | - | 列出可见窗口 |
| `window_activate` | `hwnd` 或 `title` | 激活窗口（title 为包含匹配） |
| `clipboard_get` | - | 读剪贴板 |
| `clipboard_set` | `text` | 写剪贴板 |
| `file_read` | `path, base64=false` | 读文件（32MB 上限） |
| `file_write` | `path, content` 或 `base64` | 写文件（自动建父目录） |
| `dir_list` | `path` | 列目录 |
| `launch` | `path, args=[]` | 启动程序，不等待退出，返回 pid |
| `use_card` | `card_id` | 执行技能卡内步骤（递归上限 3 层，防循环） |

### 示例：截屏 + 复制 + 读剪贴板

```json
{"steps": [
  {"action": "screenshot", "quality": 70},
  {"action": "key", "keys": ["ctrl", "c"]},
  {"action": "clipboard_get"}
]}
```

## 技能卡

技能卡文件：`%APPDATA%\GSIC\skill_cards.json`，与 Android 版同格式：

```json
{"cards": [{
  "id": "card_open_notepad",
  "name": "打开记事本",
  "trigger_words": ["记事本", "打开记事本"],
  "steps": [
    {"action": "launch", "path": "notepad.exe"},
    {"action": "sleep", "ms": 800}
  ]
}]}
```

也可以通过 `/cards` 接口增改（见 examples/skill_card_example.json）。

## 图形化界面

原生 Windows 窗口界面（Walk 库，纯 Go，无 CGO）：

- **托盘图标**：右键菜单有"显示主界面"、"检查更新"、"重启服务"、"退出"；左键双击显示主窗口
- **主窗口**：800x600，六个标签页——状态（运行/Tailscale/Token/更新）、
  连接智能体（二维码 + 一键复制提示词/地址）、设备（本机信息）、
  设置（端口/自动更新/开机自启/更新源）、日志（最近 100 行）、关于；
  关闭按钮最小化到托盘，不退出程序
- **开机自启**：设置页勾选后写入注册表 Run 键
- 无界面运行：加 `-no-gui` 参数（纯控制台，适合服务器/计划任务）
- v4.35 的网页控制面板（/ui）已删除，不再需要浏览器

## 自更新

- **自动检查**：启动时检查一次，24 小时节流，只记日志不打扰；
  `/status` 返回 `update_available` 和 `latest_version`
- **更新源**：默认 GitHub Release 的 `version-windows.json`
  （`releases/latest/download/version-windows.json`）；每个 Release 都附该文件
- **手动更新**：托盘选"检查更新"，或 POST `/update/check`、`/update/apply`
- **推送自动安装**：Muse 通过 POST `/update/push` 推送新版本
  （body `{"version","url","sha256"}`）；`auto_install_updates` 开时
  （默认开）自动下载、验 hash、替换、重启；关时只记录待手动确认
- **安全**：只从 HTTPS 源下载；无 hash、hash 格式错误、hash 不匹配都拒绝安装
  （更新源的 `version-windows.json` 必须带 `sha256`，由 build.sh 生成）；
  更新前自动备份旧 exe，替换失败时自动恢复
- **配置**：config.json 中 `auto_update`（默认 true）、`auto_install_updates`
  （默认 true）、`update_source`（默认 GitHub version-windows.json）
- **禁用**：`-no-auto-update` 参数，或 config.json 中 `auto_update: false`

## 安全注意

- token 是唯一鉴权凭证，不要发给任何人、不要提交到 Git。
- Tailscale 登录链接是 bearer 凭证，只显示在界面/日志里，绝不进 HTTP 响应。
- `/shell` 能执行任意命令，拿到 token 就等于拿到这台电脑的控制权。
- 换 token：`gsic-x.xx-windows-x64.exe -gen-token`（旧 token 立即失效）。

## 已知限制（v4.41）

- 截图是全虚拟屏（多显示器拼成一张大图），暂不支持指定显示器/区域截图。
- `SetForegroundWindow` 有时会被 Windows 拒绝（前台窗口抢焦点限制），
  激活失败时会返回错误；可重试或先点击任务栏图标。
- 锁屏状态下无法远程输入 PIN 解锁：登录界面在安全桌面上，
  Windows 拒绝用户会话的模拟输入（`SendInput` 报 Access is denied），
  需在电脑上手动解锁；锁屏切换瞬间截图可能返回 500，重试即可。
- 以普通用户运行时无法操作"以管理员身份运行"的窗口（UIPI 隔离）；
  需要时请以管理员身份运行本程序。

## 从源码构建

Linux 上交叉编译（推荐，`build.sh` 一步到位）：

```bash
./build.sh
# 产物：gsic-<版本号>-windows-x64.exe 与 GSIC-Setup-<版本号>.exe
```

依赖：Go 1.25+、Python 3；安装包还需要 makensis（NSIS 3）。
版本号从 `main.go` 的 `Version` / `VersionCode` 读取。
图标以 base64 内嵌在 `assets/gsic-icon.b64.json`，构建时自动还原为
`.ico`（二进制不进版本跟踪）；`rsrc` 缺失时 `build.sh` 自动安装。

Windows 本机也可以直接编译：

```bash
go build -ldflags "-H windowsgui -s -w" -o gsic-windows.exe .
```

注意：`gsic_windows_amd64.syso`（应用清单 + 图标）必须由 rsrc 生成，
缺 Common-Controls v6 清单时 Walk 界面创建窗口会失败（`TTM_ADDTOOL failed`）：

```bash
python3 assets/restore_icon.py
rsrc -manifest gsic.manifest -ico assets/gsic.ico -o gsic_windows_amd64.syso
```

## 更新日志

### v4.43（2026-11-27）
- 防休眠：GSIC 运行任务（/task、/shell）时电脑不休眠/睡眠，空闲 10 分钟后恢复正常休眠（按量付费云电脑省钱）；实现两层——SetThreadExecutionState（ES_SYSTEM_REQUIRED | ES_AWAYMODE_REQUIRED）阻止 Windows 睡眠 + 每分钟一次零位移鼠标移动重置云平台的输入空闲计时（不移动光标，不干扰点击）；`/status` 新增 `keep_awake`（当前是否持有防休眠锁）

### v4.42（2026-11-26）
- Auth Key 加入通道：Tailscale Auth Key（tskey-auth-...）可替代浏览器登录入网，解决上不了外网、登录不了谷歌的机器无法加入 tailnet 的问题；入口三处——主窗口「状态」页 Tailscale 区（密码框 +「用 Key 加入」）、`POST /tailscale/authkey`、`GSIC_TAILSCALE_AUTHKEY` 环境变量；Key 存 config.json（0600），不进日志与任何响应；`/status` 新增 `tsnet_authkey_set`（只报有没有，不报 Key 本身）

### v4.41（2026-11-22）
- 「连接智能体」页：二维码（地址 + token）+ 一键复制完整提示词/地址；
  提示词模板在 `assets/agent_prompt.md`，`GET /agent-prompt` 返回填好本机信息的全文
- 推送自更新首次在真机上跑通（v4.36 的 PID 修复经实测有效）

### v4.40（2026-11-22）
- Windows 版专属图标（深色圆角 + 彩虹铬光 M + 窗格标记），内嵌进 exe 与安装包

### v4.39（2026-11-22）
- 无控制台窗口（`-H windowsgui`）；NSIS 安装包（按用户安装、无需管理员、
  开机自启、桌面快捷方式）；Tailscale 登录链接显示在界面里；
  更新源改走 GitHub Release（`version-windows.json`）

### v4.36（2026-11-22）
- 原生 Windows 图形界面（Walk 库，纯 Go，无 CGO）：主窗口 800x600，五个标签页——状态（运行/Tailscale/Token 掩码显示+复制按钮/更新检查）、设备（主机名/系统/进程/数据目录）、设置（端口/自动更新/推送自动安装/开机自启注册表/更新源）、日志（最近 100 行，刷新/清空）、关于；关闭按钮最小化到托盘；托盘图标改用 Walk 内置 NotifyIcon（左键双击显示窗口）
- 删除 v4.35 的网页控制面板（/ui 端点与 dashboard.go），不再需要浏览器
- systray 依赖移除，改用 Walk 原生托盘

### v4.35（2026-11-21）
- 图形化界面：系统托盘图标（右键菜单：显示主界面/检查更新/重启/退出）+ 网页控制面板（/ui，状态/设置/日志/关于）；`-no-gui` 可无界面运行
- 自更新：启动时 24 小时节流检查；`/update/check` 手动检查、`/update/apply` 手动应用；Muse 可经 `/update/push` 推送新版本，`auto_install_updates` 开时自动下载验 hash 替换重启；更新前自动备份、失败自动恢复；只接受 HTTPS 源
- config.json 新增 `auto_update`、`auto_install_updates`、`update_channel`、`update_source`、`last_update_check`

### v4.33（2026-11-21）
- 内置 Tailscale 节点（tsnet）：首次运行打印登录链接，浏览器授权后以 `gsic-win` 加入 tailnet；节点状态持久化，重启无需重新登录；HTTP 服务同时监听局域网与 tailnet；`/status` 返回 tsnet 状态；`-no-tsnet` 可禁用，`-hostname` 可改节点名
- 监听地址改为 0.0.0.0（v4.32 只监听 127.0.0.1，Tailscale 无法接入）

### v4.32（2026-11-21）
- 首版：Windows x64 独立 exe，HTTP 远控服务（鼠标键盘截图命令窗口文件剪贴板技能卡），与 Android 版同协议
