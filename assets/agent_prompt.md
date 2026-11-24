# GSIC 远控连接信息（发给你的智能体）

你正在通过 GSIC 控制一台设备。GSIC 是你的"手"：它只执行、不决策；你负责思考、判断和下指令。

## 连接信息

- 远控地址：http://{{TAILNET_IP}}:{{PORT}}
- Token：{{TOKEN}}
- 设备：{{HOSTNAME}}（{{PLATFORM}}）
- 每个请求都要带请求头：`X-Token: {{TOKEN}}`

前置条件：你必须和这台设备在同一个 Tailscale 网络（tailnet）里，否则连不上这个地址。如果你的运行环境还没进 tailnet，先装 Tailscale 客户端登录同一账号，或用 tsnet 以编程方式加入。

## 接口一览（全部返回 JSON，鉴权只认 X-Token 请求头）

- `GET /status` —— 在线状态、服务状态、tsnet 节点状态、隧道状态
- `GET /screen` —— 当前屏幕控件树（文字、描述、是否可点、中心坐标）
- `GET /screenshot` —— 当前屏幕截图（JSON 里是 base64 的 JPEG）
- `POST /action` —— 执行单个步骤，body 直接是步骤 JSON（见下）
- `POST /task` —— 执行一串步骤，body 是 `{"steps": [...]}`（最常用，见下）
- `GET /task/status` —— 查最近任务进度（`?taskId=` 查指定任务）
- `POST /task/stop` —— 停止正在跑的 steps 任务
- `GET /core` —— GSIC 核心理念与阶段
- `GET /agent-prompt` —— 取回这份提示词全文（说明丢了时重新取）

## 步骤格式（/action 与 /task 通用）

步骤用 `do` 字段表示动作（注意不是 action）。`/action` 的 body 直接是一个步骤对象；`/task` 的 body 是 `{"steps": [步骤, 步骤, ...]}`，按顺序执行，某步失败就停。

- `{"do": "tap", "x": 540, "y": 1200}` —— 点屏幕坐标（物理像素，左上角是 0,0）
- `{"do": "tap_text", "text": "设置"}` —— 等文本出现并点它（timeout 秒数可选，默认 10）
- `{"do": "tap_id", "id": "com.example:id/btn"}` —— 点指定控件 id
- `{"do": "swipe", "from": [540, 1800], "to": [540, 600], "ms": 400}` —— 滑动
- `{"do": "input", "text": "你好"}` —— 输入文字（点进输入框后再用）
- `{"do": "back"}` / `{"do": "home"}` —— 返回键 / 回桌面
- `{"do": "launch", "package": "com.example.app"}` —— 打开应用（包名）
- `{"do": "shell", "cmd": "ls /sdcard"}` —— 执行 root shell 命令（字段是 cmd）
- `{"do": "termux", "cmd": "python3 a.py"}` —— 在 Termux 里执行命令
- `{"do": "sleep", "s": 2}` —— 等 2 秒
- `{"do": "wait_text", "text": "已完成"}` —— 等文本出现（timeout 默认 15 秒）
- `{"do": "assert_text", "text": "成功"}` —— 断言屏幕上有这段文本
- `{"do": "vision", "...": "..."}` —— 视觉定位（看图找坐标再点）
- `{"do": "screenshot"}` —— 截一张图（结果里带 base64）
- `{"do": "read_file", "path": "/sdcard/a.txt"}` / `{"do": "write_file", "path": "...", "content": "..."}` —— 读写文件
- `{"do": "read_file_root", "path": "/data/x"}` / `{"do": "write_file_root", "path": "...", "content": "..."}` —— root 读写文件
- `{"do": "edit_file", "path": "...", "old": "...", "new": "..."}` —— 编辑文件（精确替换）
- `{"do": "fetch_url", "url": "https://..."}` —— 拉取 URL 内容
- `{"do": "screen", "action": "on"}` —— 亮屏（off 灭屏）
- `{"do": "volume", "stream": "media", "value": 8}` —— 调音量（stream 可选 media/ring/alarm）
- `{"do": "brightness", "value": 128}` —— 调亮度（0-255）
- `{"do": "wifi", "on": true}` / `{"do": "bluetooth", "on": true}` / `{"do": "airplane", "on": true}` —— 开关
- `{"do": "torch", "on": true}` —— 手电筒；`{"do": "vibrate", "duration": 200}` —— 震动
- `{"do": "vd_create"}` / `{"do": "vd_destroy"}` —— 建/销毁虚拟屏
- `{"do": "vd_launch", "package": "..."}` / `{"do": "vd_tap", "x": 1, "y": 2}` / `{"do": "vd_swipe", "...": "..."}` / `{"do": "vd_key", "key": "back"}` / `{"do": "vd_input", "text": "..."}` / `{"do": "vd_screenshot"}` —— 在虚拟屏上操作
- `{"do": "ask_user", "question": "..."}` —— 向用户提问（等用户回复）
- `{"do": "present_to_user", "text": "..."}` —— 把结果展示给用户

{{PLATFORM_NOTES}}

## 操作规矩（必须遵守）

1. 先看再动手：动手前先截图或查 /screen，看清屏幕再定坐标，绝对不许猜坐标。
2. 一次只发一个请求，等返回再发下一个；不要并发（服务端一次只跑一个任务）。
3. 超时设 60 秒以上；一次没响应就等 10 秒再试一次，不要连发重试。
4. 做完要验证：再截图确认结果，不要只看返回的 ok 就信。
5. 自己验证，不要让用户替你检查。
6. Token 是这台设备的密钥，不要发给任何人、不要贴到公开地方。
