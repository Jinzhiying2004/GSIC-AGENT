# GSIC

GSIC 是你的智能体的"手"：让你的 AI 助手（Muse 等）直接操作你的手机和电脑。
GSIC 自身不内置智能体，只执行、不决策——思考和判断都由你的智能体负责。

支持 Android 与 Windows，同一个远控协议，同一套鉴权。安装包见
[Releases](../../releases)。

## 智能体能做什么

连上 GSIC 后，你的智能体可以：

- 看屏幕（截图）、点按、滑动、输入文字、按键
- 执行系统命令、读写文件、读写剪贴板
- 列出并切换窗口、打开应用
- 调用技能卡：设备上存好的常用多步流程，一句话触发

## 快速开始（不用改代码）

这套架构按"每人一套"设计：任何人从 GitHub 下了安装包，不改代码，
按三步就能连上自己的智能体。

**第 1 步：安装**

- Android：下载 Release 里的 APK 安装
- Windows：下载 Release 里的安装包（`GSIC-Setup-x.xx.exe`）安装，无需管理员权限

**第 2 步：登录 Tailscale**

- 首次启动会显示 Tailscale 登录链接（Windows 在「连接智能体」页，
  Android 在「云端操控」页），浏览器打开链接授权，设备加入你的 tailnet，拿到 100.x 地址
- 首次启动还会自动生成 token，只存在设备本地

**第 3 步：交给智能体（只需一次）**

- Windows：打开「连接智能体」页，点「复制完整提示词」粘贴给你的智能体；
  或者把二维码拍给智能体识别
- Android：打开「云端操控」页，把 IP、端口、token 告诉你的智能体
- 提示词里有连接信息、全部接口说明和操作规矩，智能体拿到就能直接干活；
  已连上的智能体也可随时调 `GET /agent-prompt` 取回全文

**安全说明**：远控服务只放行本机回环和 Tailscale 网段（100.64.0.0/10），
同一 WiFi 下其他设备连不上——Tailscale 是远程操控的唯一通道。
不要用端口转发把端口暴露到公网。token 不要发给别人，
谁拿到 token 就能操作你的设备。

## 四大地基

- **热更新**：代码级小修复走热补丁，不用重装
- **自更新**：启动时自动检查新版本，支持智能体推送自动安装
- **自启动**：开机自动启动服务（Android 开机自启，Windows 注册表 Run 键）
- **流光彩条**（Android）：任务运行时屏幕边缘显示流光，执行过程看得见

## 构建（Android）

手工编译链（aapt2 + javac + d8 + apksigner），无需 Gradle / Android Studio。

依赖：JDK 17、Android SDK（含 `build-tools;34.0.0` 与 `platforms;android-34`）。

```bash
# 按 build.sh 头部的路径准备好 JDK 与 SDK（或按需修改）
./build.sh
# 产物：gsic-<版本号>.apk（out/ 目录）
```

构建脚本缺失 `debug.keystore` 时会自动生成调试签名。
`baselines/`（热补丁差分基线）每次构建自动归档，无需手动维护。
启动器图标以 base64 内嵌在 `tools/launcher_icons.b64.json`，构建时缺失自动还原。

最低支持 Android 8.0（API 26），完整适配 Android 11。有 root 时执行通道更完整；
无 root 走无障碍通道。

远控 API 说明见 [API.md](API.md)，架构说明见 [ARCHITECTURE.md](ARCHITECTURE.md)，
版本历史见 [CHANGELOG.md](CHANGELOG.md)。

## 版本号规则

- 每次迭代版本号 **+0.01**；单次迭代功能数超过 5 个则 **+0.1**
- `versionCode` 每次构建 +1（内部使用）
- 产物命名：Android `gsic-<版本号>.apk`，Windows `GSIC-Setup-<版本号>.exe`
- 每次发版前必须按发版测试规范实测，无法真机验证的部分如实声明

## 反馈与建议

欢迎提 [Issue](../../issues)：Bug 请附机型 / 系统版本 / 复现步骤；
功能建议请说明使用场景。我们会定期 triage 并安排进夜间迭代。

## License

[MIT](LICENSE)
