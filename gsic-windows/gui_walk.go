//go:build windows

package main

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/lxn/walk"
	. "github.com/lxn/walk/declarative"
)

// WalkMainWindow is the native Windows GUI for GSIC.
// It replaces the web dashboard (/ui) with a real Win32 window:
// status, devices, settings, log and about tabs, all built with
// the Walk library (pure Go, no CGO, cross-compiles from Linux).
type WalkMainWindow struct {
	*walk.MainWindow

	cfg     *Config
	cards   *CardStore
	updater *Updater
	tsnet   *TsnetNode
	gui     *GUI

	// Status tab widgets (updated by refreshStatus).
	lblRunState   *walk.Label
	lblTsnetState *walk.Label
	lblTsnetIP    *walk.Label
	lblPort       *walk.Label
	lblVersion    *walk.Label
	lblToken      *walk.Label
	lblUptime     *walk.Label
	lblCards      *walk.Label
	lblScreen     *walk.Label
	lblMouse      *walk.Label
	lblUpdate     *walk.Label
	leLoginURL    *walk.LineEdit
	btnLoginURL   *walk.PushButton
	loginShown     bool
	leAuthKey      *walk.LineEdit
	lblAuthKey     *walk.Label
	btnAuthKey     *walk.PushButton

	// Connect tab widgets.
	ivQR          *walk.ImageView
	leRemoteAddr  *walk.LineEdit
	leTokenShow   *walk.LineEdit
	cbShowToken   *walk.CheckBox
	qrIP          string

	// Settings tab widgets.
	nePort        *walk.NumberEdit
	cbAutoUpdate  *walk.CheckBox
	cbAutoInstall *walk.CheckBox
	cbAutostart   *walk.CheckBox
	leUpdateSrc   *walk.LineEdit

	// Log tab widget.
	teLog *walk.TextEdit
}

// NewWalkMainWindow builds the main window (does not show it yet).
// Call Show() to display it, or Run() to block on the message loop.
func NewWalkMainWindow(cfg *Config, cards *CardStore, updater *Updater, gui *GUI) (*WalkMainWindow, error) {
	mw := &WalkMainWindow{
		cfg: cfg, cards: cards, updater: updater, gui: gui,
	}

	var tabWidget *walk.TabWidget

	// App icon (embedded in the exe by rsrc). rsrc numbers resources in
	// order: the manifest takes ID 1, so the icon group is ID 2 (verified
	// with pefile 2026-11-22). Missing icon is not fatal: the window just
	// gets the default icon.
	appIcon, iconErr := walk.NewIconFromResourceId(2)
	if iconErr != nil {
		Logf("WARN: app icon unavailable (%v)", iconErr)
		appIcon = nil
	}

	err := MainWindow{
		AssignTo: &mw.MainWindow,
		Title:    fmt.Sprintf("GSIC %s - Muse's hands", Version),
		Icon:     appIcon,
		MinSize:  Size{Width: 700, Height: 520},
		Size:     Size{Width: 800, Height: 600},
		Layout:   VBox{},
		Children: []Widget{
			TabWidget{
				AssignTo: &tabWidget,
				Pages: []TabPage{
					{
						Title:  "状态",
						Layout: VBox{},
						Children: []Widget{
							GroupBox{
								Title:  "运行状态",
								Layout: Grid{Columns: 2},
								Children: []Widget{
									Label{Text: "运行状态："},
									Label{AssignTo: &mw.lblRunState, Text: "正在运行"},
									Label{Text: "版本："},
									Label{AssignTo: &mw.lblVersion, Text: Version},
									Label{Text: "监听端口："},
									Label{AssignTo: &mw.lblPort, Text: fmt.Sprintf("%d", cfg.Port)},
									Label{Text: "运行时间："},
									Label{AssignTo: &mw.lblUptime, Text: "-"},
									Label{Text: "技能卡："},
									Label{AssignTo: &mw.lblCards, Text: "-"},
									Label{Text: "屏幕："},
									Label{AssignTo: &mw.lblScreen, Text: "-"},
									Label{Text: "鼠标："},
									Label{AssignTo: &mw.lblMouse, Text: "-"},
								},
							},
							GroupBox{
								Title:  "Tailscale",
								Layout: Grid{Columns: 2},
								Children: []Widget{
									Label{Text: "节点状态："},
									Label{AssignTo: &mw.lblTsnetState, Text: "-"},
									Label{Text: "Tailnet IP："},
									Label{AssignTo: &mw.lblTsnetIP, Text: "-"},
									Label{Text: "登录链接："},
									Composite{
										Layout: HBox{},
										Children: []Widget{
											LineEdit{AssignTo: &mw.leLoginURL, ReadOnly: true, Text: "-"},
											PushButton{
												AssignTo: &mw.btnLoginURL,
												Text:     "浏览器打开",
												Enabled:  false,
												OnClicked: func() {
													if mw.tsnet != nil {
														if u := mw.tsnet.LoginURL(); u != "" {
															openURL(u)
														}
													}
												},
											},
										},
									},
									Label{Text: "Auth Key："},
									Composite{
										Layout: HBox{},
										Children: []Widget{
											LineEdit{AssignTo: &mw.leAuthKey, PasswordMode: true, Text: ""},
											PushButton{
												AssignTo: &mw.btnAuthKey,
												Text:     "用 Key 加入",
												OnClicked: func() {
													mw.applyAuthKey()
												},
											},
										},
									},
									Label{Text: "Key 状态："},
									Label{AssignTo: &mw.lblAuthKey, Text: "-"},
								},
							},
							Label{Text: "登录不了谷歌时：去 Tailscale 管理控制台（login.tailscale.com）→ 设置 → 密钥，生成 Auth Key 粘贴到上面，点「用 Key 加入」即可入网，不用浏览器登录。"},
							GroupBox{
								Title:  "安全",
								Layout: Grid{Columns: 2},
								Children: []Widget{
									Label{Text: "Token："},
									Label{AssignTo: &mw.lblToken, Text: maskToken(cfg.Token)},
									Label{Text: ""},
									PushButton{
										Text: "复制 Token",
										OnClicked: func() {
											mw.copyToken()
										},
									},
								},
							},
							GroupBox{
								Title:  "更新",
								Layout: HBox{},
								Children: []Widget{
									Label{AssignTo: &mw.lblUpdate, Text: "检查更新状态…"},
									PushButton{
										Text: "检查更新",
										OnClicked: func() {
											mw.checkUpdate()
										},
									},
								},
							},
							VSpacer{},
						},
					},
					{
						Title:  "连接智能体",
						Layout: VBox{},
						Children: []Widget{
							Label{Text: "把二维码拍给你的智能体，或点下面的按钮复制粘贴给它，只需一次。"},
							GroupBox{
								Title:  "二维码",
								Layout: VBox{},
								Children: []Widget{
									ImageView{
										AssignTo: &mw.ivQR,
										Mode:     ImageViewModeShrink,
										MinSize:  Size{Width: 280, Height: 280},
									},
								},
							},
							GroupBox{
								Title:  "连接信息",
								Layout: Grid{Columns: 2},
								Children: []Widget{
									Label{Text: "远控地址："},
									LineEdit{AssignTo: &mw.leRemoteAddr, ReadOnly: true, Text: "-"},
									Label{Text: "Token："},
									Composite{
										Layout: HBox{},
										Children: []Widget{
											LineEdit{AssignTo: &mw.leTokenShow, ReadOnly: true, Text: maskToken(cfg.Token)},
											CheckBox{
												AssignTo: &mw.cbShowToken,
												Text:     "显示",
												OnCheckedChanged: func() {
													mw.toggleTokenShow()
												},
											},
										},
									},
								},
							},
							Composite{
								Layout: HBox{},
								Children: []Widget{
									PushButton{
										Text: "复制完整提示词",
										OnClicked: func() {
											mw.copyAgentPrompt()
										},
									},
									PushButton{
										Text: "复制地址和 Token",
										OnClicked: func() {
											mw.copyConnectShort()
										},
									},
								},
							},
							Label{Text: "提示：二维码和提示词里都包含 Token，只发给你的智能体。"},
							VSpacer{},
						},
					},
					{
						Title:  "设备",
						Layout: VBox{},
						Children: []Widget{
							GroupBox{
								Title:  "本机信息",
								Layout: Grid{Columns: 2},
								Children: []Widget{
									Label{Text: "主机名："},
									Label{Text: mustHostname()},
									Label{Text: "系统："},
									Label{Text: fmt.Sprintf("%s / %s", runtime.GOOS, runtime.GOARCH)},
									Label{Text: "Go 版本："},
									Label{Text: runtime.Version()},
									Label{Text: "进程 ID："},
									Label{Text: fmt.Sprintf("%d", os.Getpid())},
									Label{Text: "数据目录："},
									Label{Text: cfg.DataDir},
								},
							},
							VSpacer{},
						},
					},
					{
						Title:  "设置",
						Layout: VBox{},
						Children: []Widget{
							GroupBox{
								Title:  "网络",
								Layout: Grid{Columns: 2},
								Children: []Widget{
									Label{Text: "监听端口："},
									NumberEdit{
										AssignTo: &mw.nePort,
										Value:    float64(cfg.Port),
										MinValue: 1,
										MaxValue: 65535,
									},
								},
							},
							GroupBox{
								Title:  "更新",
								Layout: VBox{},
								Children: []Widget{
									CheckBox{
										AssignTo: &mw.cbAutoUpdate,
										Text:     "自动检查更新（每 24 小时）",
										Checked:  cfg.AutoUpdate,
									},
									CheckBox{
										AssignTo: &mw.cbAutoInstall,
										Text:     "推送自动安装（收到推送后自动安装并重启）",
										Checked:  cfg.AutoInstallUpdates,
									},
									Label{Text: "更新源 URL（留空用默认）："},
									LineEdit{
										AssignTo: &mw.leUpdateSrc,
										Text:     cfg.UpdateSource,
									},
								},
							},
							GroupBox{
								Title:  "系统",
								Layout: VBox{},
								Children: []Widget{
									CheckBox{
										AssignTo: &mw.cbAutostart,
										Text:     "开机自启（写入注册表 Run 键）",
										Checked:  isAutostartEnabled(),
									},
								},
							},
							PushButton{
								Text: "保存设置",
								OnClicked: func() {
									mw.saveSettings()
								},
							},
							Label{Text: "提示：端口修改需重启 GSIC 后生效。"},
							VSpacer{},
						},
					},
					{
						Title:  "日志",
						Layout: VBox{},
						Children: []Widget{
							TextEdit{
								AssignTo: &mw.teLog,
								ReadOnly: true,
								VScroll:  true,
							},
							Composite{
								Layout: HBox{},
								Children: []Widget{
									PushButton{
										Text: "刷新",
										OnClicked: func() {
											mw.refreshLog()
										},
									},
									PushButton{
										Text: "清空",
										OnClicked: func() {
											mw.teLog.SetText("")
										},
									},
									HSpacer{},
								},
							},
						},
					},
					{
						Title:  "关于",
						Layout: VBox{},
						Children: []Widget{
							Label{Text: fmt.Sprintf("GSIC %s (build %d)", Version, VersionCode), Font: Font{Bold: true, PointSize: 14}},
							Label{Text: "Muse 的手，运行在 Windows PC 上。只执行、不决策。"},
							Label{Text: ""},
							Label{Text: "GitHub："},
							PushButton{
								Text: "https://github.com/Jinzhiying2004/GSIC-AGENT",
								OnClicked: func() {
									openURL("https://github.com/Jinzhiying2004/GSIC-AGENT")
								},
							},
							Label{Text: ""},
							Label{Text: "作者：靳志英 (Jin Zhiying)"},
							Label{Text: "平台：Windows 10/11 x64"},
							VSpacer{},
						},
					},
				},
			},
		},
	}.Create()

	if err != nil {
		return nil, fmt.Errorf("create main window: %w", err)
	}

	// Close button minimizes to tray instead of quitting.
	mw.Closing().Attach(func(canceled *bool, reason walk.CloseReason) {
		*canceled = true
		mw.Hide()
	})

	// Initial refresh.
	mw.refreshStatus()
	mw.refreshLog()

	// Auto-refresh status every 5 seconds.
	go func() {
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		for range ticker.C {
			if mw.MainWindow == nil {
				return
			}
			mw.Synchronize(func() {
				mw.refreshStatus()
			})
		}
	}()

	return mw, nil
}

// SetTsnet attaches the Tailscale node for status display.
func (mw *WalkMainWindow) SetTsnet(n *TsnetNode) {
	mw.tsnet = n
	mw.refreshStatus()
}

// Show displays the window (brings to front if already visible).
func (mw *WalkMainWindow) Show() {
	mw.refreshStatus()
	mw.refreshLog()
	mw.MainWindow.Show()
	mw.MainWindow.SetFocus()
}

// refreshStatus updates all status tab labels.
func (mw *WalkMainWindow) refreshStatus() {
	if mw.lblRunState == nil {
		return
	}
	mw.lblRunState.SetText("正在运行")
	mw.lblVersion.SetText(fmt.Sprintf("%s (build %d)", Version, VersionCode))
	mw.lblPort.SetText(fmt.Sprintf("%d", mw.cfg.Port))
	mw.lblUptime.SetText(formatUptime(time.Since(startTime)))
	if mw.cards != nil {
		mw.lblCards.SetText(fmt.Sprintf("%d 张", len(mw.cards.List())))
	}
	sw, sh := ScreenSize()
	mw.lblScreen.SetText(fmt.Sprintf("%dx%d", sw, sh))
	mx, my := GetMousePos()
	mw.lblMouse.SetText(fmt.Sprintf("%d, %d", mx, my))

	if mw.tsnet != nil {
		mw.lblTsnetState.SetText(mw.tsnet.State())
		ip := mw.tsnet.TailIP()
		if ip == "" {
			ip = "-"
		}
		mw.lblTsnetIP.SetText(ip)
		if mw.cfg.TailscaleAuthKey != "" {
			mw.lblAuthKey.SetText("已保存")
		} else {
			mw.lblAuthKey.SetText("未设置")
		}
		// First-run login: show the auth URL (bearer credential, GUI
		// only, never over HTTP) and pop the window once so the user
		// sees it. No console exists in the installed build.
		if url := mw.tsnet.LoginURL(); url != "" {
			mw.leLoginURL.SetText(url)
			mw.btnLoginURL.SetEnabled(true)
			if !mw.loginShown {
				mw.loginShown = true
				mw.Show()
			}
		} else if mw.tsnet.IsRunning() {
			mw.leLoginURL.SetText("已登录")
			mw.btnLoginURL.SetEnabled(false)
		} else {
			mw.leLoginURL.SetText("等待连接…")
			mw.btnLoginURL.SetEnabled(false)
		}
	} else {
		mw.lblTsnetState.SetText("disabled")
		mw.lblTsnetIP.SetText("-")
		mw.leLoginURL.SetText("-")
		mw.btnLoginURL.SetEnabled(false)
	}

	if mw.updater != nil {
		st := mw.updater.Status()
		if st.Available {
			mw.lblUpdate.SetText(fmt.Sprintf("发现新版本 %s (build %d)", st.LatestVersion, st.LatestCode))
		} else if !st.CheckedAt.IsZero() {
			mw.lblUpdate.SetText(fmt.Sprintf("已是最新（上次检查 %s）", st.CheckedAt.Format("15:04")))
		} else {
			mw.lblUpdate.SetText("尚未检查")
		}
	}

	mw.refreshQR()
}

// refreshQR regenerates the connect QR code when the tailnet IP changes.
func (mw *WalkMainWindow) refreshQR() {
	if mw.ivQR == nil {
		return
	}
	ip := ""
	if mw.tsnet != nil {
		ip = mw.tsnet.TailIP()
	}
	if ip == "" {
		mw.leRemoteAddr.SetText("-")
		return
	}
	mw.leRemoteAddr.SetText(fmt.Sprintf("http://%s:%d", ip, mw.cfg.Port))
	if ip == mw.qrIP {
		return
	}
	bmp, err := qrBitmap(ip, mw.cfg.Port, mw.cfg.Token, 512)
	if err != nil {
		Logf("WARN: QR code failed (%v)", err)
		return
	}
	if err := mw.ivQR.SetImage(bmp); err != nil {
		Logf("WARN: QR display failed (%v)", err)
		return
	}
	mw.qrIP = ip
}

// toggleTokenShow switches the connect tab token line between masked
// and full text.
func (mw *WalkMainWindow) toggleTokenShow() {
	if mw.cbShowToken.Checked() {
		mw.leTokenShow.SetText(mw.cfg.Token)
	} else {
		mw.leTokenShow.SetText(maskToken(mw.cfg.Token))
	}
}

// copyAgentPrompt copies the full agent prompt (connection info +
// instructions) to the clipboard: one paste and the agent knows
// everything.
func (mw *WalkMainWindow) copyAgentPrompt() {
	ip := ""
	if mw.tsnet != nil {
		ip = mw.tsnet.TailIP()
	}
	if ip == "" {
		walk.MsgBox(mw.MainWindow, "还没连上", "Tailscale 还没拿到地址，先完成登录再复制。", walk.MsgBoxIconWarning)
		return
	}
	if err := SetClipboardText(FillAgentPrompt(mw.cfg, ip)); err != nil {
		walk.MsgBox(mw.MainWindow, "复制失败", fmt.Sprintf("无法复制：%v", err), walk.MsgBoxIconError)
		return
	}
	walk.MsgBox(mw.MainWindow, "已复制", "完整提示词已复制到剪贴板，粘贴给你的智能体即可。", walk.MsgBoxIconInformation)
}

// copyConnectShort copies just the address and token.
func (mw *WalkMainWindow) copyConnectShort() {
	ip := ""
	if mw.tsnet != nil {
		ip = mw.tsnet.TailIP()
	}
	if ip == "" {
		walk.MsgBox(mw.MainWindow, "还没连上", "Tailscale 还没拿到地址，先完成登录再复制。", walk.MsgBoxIconWarning)
		return
	}
	text := fmt.Sprintf("远控地址：http://%s:%d\nToken：%s", ip, mw.cfg.Port, mw.cfg.Token)
	if err := SetClipboardText(text); err != nil {
		walk.MsgBox(mw.MainWindow, "复制失败", fmt.Sprintf("无法复制：%v", err), walk.MsgBoxIconError)
		return
	}
	walk.MsgBox(mw.MainWindow, "已复制", "地址和 Token 已复制到剪贴板。", walk.MsgBoxIconInformation)
}

// applyAuthKey saves the pasted Tailscale auth key and rejoins the
// tailnet with it (no browser login needed).
func (mw *WalkMainWindow) applyAuthKey() {
	key := strings.TrimSpace(mw.leAuthKey.Text())
	if key == "" {
		walk.MsgBox(mw.MainWindow, "没有 Key", "先粘贴 Auth Key（tskey-auth- 开头）。", walk.MsgBoxIconWarning)
		return
	}
	if !ValidAuthKey(key) {
		walk.MsgBox(mw.MainWindow, "Key 不对", "这不是 Auth Key：应以 tskey-auth- 开头。注意是 Auth Key，不是 API Key（tskey-api-）。", walk.MsgBoxIconWarning)
		return
	}
	if mw.tsnet == nil {
		walk.MsgBox(mw.MainWindow, "Tailscale 未启用", "内置 Tailscale 节点未启用，无法用 Key 加入。", walk.MsgBoxIconWarning)
		return
	}
	mw.cfg.TailscaleAuthKey = key
	if err := mw.cfg.Save(); err != nil {
		walk.MsgBox(mw.MainWindow, "保存失败", fmt.Sprintf("无法保存配置：%v", err), walk.MsgBoxIconError)
		return
	}
	mw.leAuthKey.SetText("")
	if mw.tsnet.IsRunning() {
		mw.refreshStatus()
		walk.MsgBox(mw.MainWindow, "已在通道中", "本机已在通道里，Key 已保存，下次启动备用。", walk.MsgBoxIconInformation)
		return
	}
	if _, err := mw.tsnet.RestartWithAuthKey(key); err != nil {
		walk.MsgBox(mw.MainWindow, "加入失败", err.Error(), walk.MsgBoxIconError)
		return
	}
	mw.refreshStatus()
	walk.MsgBox(mw.MainWindow, "正在加入", "Key 已保存，正在用 Key 重新加入通道，稍等片刻看状态。", walk.MsgBoxIconInformation)
}

// refreshLog reloads the log text from the GUI buffer.
func (mw *WalkMainWindow) refreshLog() {
	if mw.teLog == nil || mw.gui == nil {
		return
	}
	lines := mw.gui.LogLines()
	if len(lines) > 100 {
		lines = lines[len(lines)-100:]
	}
	mw.teLog.SetText(strings.Join(lines, "\r\n"))
}

// copyToken copies the full token to the clipboard.
func (mw *WalkMainWindow) copyToken() {
	if err := SetClipboardText(mw.cfg.Token); err != nil {
		walk.MsgBox(mw.MainWindow, "复制失败", fmt.Sprintf("无法复制 Token：%v", err), walk.MsgBoxIconError)
		return
	}
	walk.MsgBox(mw.MainWindow, "已复制", "Token 已复制到剪贴板，请妥善保管。", walk.MsgBoxIconInformation)
}

// checkUpdate triggers a manual update check.
func (mw *WalkMainWindow) checkUpdate() {
	if mw.updater == nil {
		return
	}
	go func() {
		st := mw.updater.Check()
		mw.Synchronize(func() {
			if st.Error != "" {
				walk.MsgBox(mw.MainWindow, "检查失败", st.Error, walk.MsgBoxIconError)
				return
			}
			if st.Available {
				if walk.MsgBox(mw.MainWindow, "发现新版本",
					fmt.Sprintf("新版本 %s (build %d) 可用，是否立即更新？", st.LatestVersion, st.LatestCode),
					walk.MsgBoxYesNo|walk.MsgBoxIconQuestion) == walk.DlgCmdYes {
					mw.applyUpdate()
				}
			} else {
				walk.MsgBox(mw.MainWindow, "已是最新", fmt.Sprintf("当前已是最新版本 %s。", Version), walk.MsgBoxIconInformation)
			}
			mw.refreshStatus()
		})
	}()
}

// applyUpdate downloads and applies the update (restarts GSIC).
func (mw *WalkMainWindow) applyUpdate() {
	if mw.updater == nil {
		return
	}
	go func() {
		err := mw.updater.Apply()
		mw.Synchronize(func() {
			if err != nil {
				walk.MsgBox(mw.MainWindow, "更新失败", err.Error(), walk.MsgBoxIconError)
			}
			// On success the updater restarts the process; no dialog needed.
		})
	}()
}

// saveSettings writes the settings tab values to config.json.
func (mw *WalkMainWindow) saveSettings() {
	port := int(mw.nePort.Value())
	if port < 1 || port > 65535 {
		walk.MsgBox(mw.MainWindow, "端口无效", "端口必须在 1-65535 之间。", walk.MsgBoxIconWarning)
		return
	}
	mw.cfg.Port = port
	mw.cfg.AutoUpdate = mw.cbAutoUpdate.Checked()
	mw.cfg.AutoInstallUpdates = mw.cbAutoInstall.Checked()
	mw.cfg.UpdateSource = mw.leUpdateSrc.Text()

	if err := mw.cfg.Save(); err != nil {
		walk.MsgBox(mw.MainWindow, "保存失败", fmt.Sprintf("无法保存配置：%v", err), walk.MsgBoxIconError)
		return
	}

	// Autostart: write/remove the registry Run key.
	if mw.cbAutostart.Checked() {
		if err := setAutostart(true); err != nil {
			walk.MsgBox(mw.MainWindow, "开机自启失败", fmt.Sprintf("无法设置开机自启：%v", err), walk.MsgBoxIconWarning)
		}
	} else {
		_ = setAutostart(false)
	}

	mw.refreshStatus()
	walk.MsgBox(mw.MainWindow, "已保存", "设置已保存。端口修改需重启 GSIC 后生效。", walk.MsgBoxIconInformation)
}

// ---- helpers ----

func maskToken(tok string) string {
	if len(tok) <= 8 {
		return "****"
	}
	return tok[:4] + "..." + tok[len(tok)-4:]
}

func mustHostname() string {
	h, err := os.Hostname()
	if err != nil {
		return "-"
	}
	return h
}

func formatUptime(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%d 秒", int(d.Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("%d 分钟", int(d.Minutes()))
	}
	if d < 24*time.Hour {
		return fmt.Sprintf("%d 小时 %d 分钟", int(d.Hours()), int(d.Minutes())%60)
	}
	return fmt.Sprintf("%d 天 %d 小时", int(d.Hours())/24, int(d.Hours())%24)
}

func openURL(url string) {
	_ = exec.Command("rundll32", "url.dll,FileProtocolHandler", url).Start()
}
