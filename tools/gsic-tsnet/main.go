// gsic-tsnet: GSIC 内置 Tailscale 节点小进程（原创实现，仅使用 tsnet 公开 API）。
//
// 职责：加入用户的 tailnet，在 tailnet 上监听一个端口，把拨入流量原样转发到
// 本机回环的 GSIC 远控服务（默认 127.0.0.1:28789）。传输层只换 tailnet，
// 鉴权仍由 GSIC 远控服务自己的 X-Token 负责，本进程不做任何鉴权放行。
//
// 与 GSIC Java 层的协议（stdout 行协议，Java 只解析带 TSNET_ 前缀的行）：
//
//	TSNET_IP=<tailnet IP>   监听就绪后打印一次
//	TSNET_READY             监听就绪
//	TSNET_ERROR=<msg>       启动失败，进程随即退出
//	TSNET_LOGIN_URL=<url>   网页登录模式：控制面下发的授权链接（只走 stdout
//	                        给 Java 层卡面展示，绝不进任何日志文件；链接变了
//	                        就再报一次新的）
//	TSNET_LOGIN_EXPIRED     网页登录模式：上次报出的链接失效（控制面收回链接，
//	                        或同一链接 15 分钟未完成授权——15 分钟是本进程的
//	                        客户端侧过期提示阈值，不是控制面 TTL 的断言）；
//	                        Java 层可重启进程重拉新链接
//	TSNET_STATUS=<后端状态>|<脱敏类别>  网页登录等待期间每 2 秒一行，如
//	                        NeedsLogin|正在注册；只含状态词与脱敏后的错误
//	                        类别（控制面 400/超时/DNS 失败等），绝不含密钥、
//	                        链接、节点密钥；Java 层卡面展示，替代干巴巴的
//	                        “正在拉取”
//
// 其余 tsnet 内部日志走 stderr，Java 侧忽略。
//
// v3.89：致命启动错误统一经 TSNET_ERROR= 上报（脱敏后首行）；主 goroutine
// 的 panic 经 recover 同样上报 TSNET_ERROR=panic:<首行> 再退出；其它
// goroutine 的 panic 由 Java 侧退出码 + 原始输出尾行兜底。
//
// 停止：stdin 收到 STOP 行、或收到 SIGINT/SIGTERM 时优雅关闭监听与节点，
// 退出码 0。Java 侧 stop 时先写 STOP，短暂等待后仍活着才 destroy。
//
// 网页登录模式：无 auth key 时不再拒绝启动，而是启动节点后
// 轮询 LocalClient Status：BackendState 为 NeedsLogin 且 AuthURL 非空时
// 上报链接并继续等；BackendState 到 Running 且拿到 tailnet IP 后才 Listen
// 并报 READY。已登录过时 Status 直接就是 Running，立即回 IP 不出链接。
//
// 密钥：auth key 不进命令行参数（ps 可见），只从环境变量 TSNET_AUTHKEY
// 或 -authkey 参数读取；节点长期状态（登录后无需重复登录）存在 -dir
// 指定的私有目录，Java 侧传 noBackupFilesDir 下的目录，避免备份恢复后
// 冒充另一台设备。auth key 与登录链接绝不打印到任何日志输出。
package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"tailscale.com/client/local"
	"tailscale.com/ipn"
	"tailscale.com/tsnet"
)

const version = "gsic-tsnet 1.0.0"

// emit：stdout 行协议统一出口。logf 回调与等待循环并发写 stdout 时，
// 互斥保证每行完整不交错；stderr 日志行不受此约束。
var outMu sync.Mutex

func emit(line string) {
	outMu.Lock()
	defer outMu.Unlock()
	fmt.Println(line)
}

// loginReporter：登录等待期的共享状态，Status 轮询与 logf 回调两路共用。
// 互斥保证“任一拿到链接即上报、不互相覆盖已上报的有效链接”。
type loginReporter struct {
	mu              sync.Mutex
	lastURL         string
	urlSince        time.Time
	expiredReported bool
	activity        string
}

// reportURL：新链接才上报（去重）；链接只走 TSNET_LOGIN_URL 行给 Java 层，
// 绝不进 stderr 日志。
func (r *loginReporter) reportURL(url string) {
	if url == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if url == r.lastURL {
		return
	}
	r.lastURL = url
	r.urlSince = time.Now()
	r.expiredReported = false
	emit("TSNET_LOGIN_URL=" + url)
}

// checkExpired：上次报出的链接被控制面收回（本轮 AuthURL 为空）、或同一
// 链接 15 分钟未完成授权时，报一次 TSNET_LOGIN_EXPIRED。
func (r *loginReporter) checkExpired(curURL string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.lastURL != "" && !r.expiredReported &&
		(curURL == "" || time.Since(r.urlSince) > 15*time.Minute) {
		r.expiredReported = true
		emit("TSNET_LOGIN_EXPIRED")
	}
}

// setActivity：只记脱敏后的错误类别词，绝不记原始行（原始行可能含链接、
// 节点密钥等敏感内容）。
func (r *loginReporter) setActivity(cat string) {
	if cat == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.activity = cat
}

func (r *loginReporter) getActivity() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.activity
}

// classifyActivity：把一行内部日志/错误映射为脱敏类别词；无匹配返回空
// （调用方保留上一次类别，不用噪声覆盖）。
func classifyActivity(s string) string {
	low := strings.ToLower(s)
	switch {
	case strings.Contains(s, "400") || strings.Contains(low, "bad request"):
		return "控制面 400"
	case strings.Contains(low, "timed out") || strings.Contains(low, "timeout") || strings.Contains(low, "deadline exceeded"):
		return "超时"
	case strings.Contains(low, "no such host") || strings.Contains(low, "lookup ") || strings.Contains(low, "dns"):
		return "DNS 失败"
	case strings.Contains(low, "connection refused"):
		return "连接被拒"
	case strings.Contains(low, "network unreachable"):
		return "网络不可达"
	case strings.Contains(low, "register"):
		return "正在注册"
	default:
		return ""
	}
}

func main() {
	defer func() {
		if r := recover(); r != nil {
			// 主 goroutine 的 panic：先报 TSNET_ERROR 再退出；其它 goroutine
			// 的 panic 由 Java 侧退出码 + 原始输出尾行兜底（见 TsnetNode）。
			emit("TSNET_ERROR=panic:" + sanitizeOneLine(fmt.Sprint(r)))
			os.Exit(1)
		}
	}()
	os.Exit(realMain())
}

// sanitizeOneLine：致命错误只取首行、截断 200 字；含链接/密钥嫌疑的
// 一律替换为脱敏占位，绝不把链接或 key 原文打进任何输出。
func sanitizeOneLine(s string) string {
	if i := strings.Index(s, "\n"); i >= 0 {
		s = s[:i]
	}
	s = strings.TrimSpace(s)
	low := strings.ToLower(s)
	for _, banned := range []string{"go to:", "tsnet_login_url", "tsnet_authkey", "authkey", "login.tailscale.com", "http://", "https://"} {
		if strings.Contains(low, banned) {
			return "已脱敏"
		}
	}
	if len(s) > 200 {
		s = s[:200]
	}
	return s
}

func realMain() int {
	// v3.94：进程一启动就 setsid 脱离 App 进程组。但 3.94 在真机上
	// 仍未根治——幽灵进程机制按 App UID（不是会话/进程组）认子进程，
	// 只要进程还是 App 进程直接 fork 出来的、UID 还是 App 的，
	// 换了会话也照样算幽灵进程杀掉；root 起的测试进程能活下来，
	// 正因为它的 UID 是 root、不在幽灵进程的猎杀范围。
	// v4.09 的根治在 Java 侧：Android 12+ 且有 root 时，Java 不再
	// 用 ProcessBuilder 直接 fork，而是经 root shell 起本进程
	// （UID=root、父进程是 su shell），幽灵进程机制根本不看它；
	// 通信随之从 stdout 管道改成协议文件 + pid 文件 + 信号，
	// Go 侧只补三件小事：写 pid 文件、读密钥文件、忽略 SIGHUP。
	// 若已是会话首领则 Setsid 报错，那本身就是已脱离状态，直接
	// 忽略继续跑；非 root 回退路径仍走旧 ProcessBuilder，Setsid
	// 在那里仍是唯一能做的脱离手段，保留。
	_, _ = syscall.Setsid()
	signal.Ignore(syscall.SIGHUP)
	showVersion := flag.Bool("version", false, "print version and exit")
	authKey := flag.String("authkey", "", "Tailscale auth key (prefer env TSNET_AUTHKEY)")
	stateDir := flag.String("dir", "", "node state directory (required)")
	hostname := flag.String("hostname", "gsic", "tailnet node hostname")
	forward := flag.String("forward", "127.0.0.1:28789", "local GSIC remote service to forward to")
	listenPort := flag.Int("port", 28789, "tailnet listen port")
	flag.Parse()

	if *showVersion {
		fmt.Println(version)
		return 0
	}
	if *stateDir == "" {
		fmt.Fprintln(os.Stderr, "TSNET_ERROR=missing -dir")
		emit("TSNET_ERROR=missing -dir")
		return 1
	}
	key := os.Getenv("TSNET_AUTHKEY")
	if key == "" {
		key = *authKey
	}
	// v4.09：root 守护路径的密钥不走命令行（ps 可见）也不走环境
	// （root shell 命令行里写密钥等于把密钥写进 Java 侧命令串），
	// Java 侧把密钥写进状态目录下的 authkey 文件（600、仅属主可读，
	// root 进程可读），Go 在这里读；读到就当密钥模式，读不到才走
	// 网页登录。文件内容绝不打印，读完不留痕在任何输出里。
	if key == "" {
		if b, err := os.ReadFile(*stateDir + "/authkey"); err == nil {
			key = strings.TrimSpace(string(b))
		}
	}
	// v4.09：写 pid 文件（644，Java 侧靠它认进程、发信号、判存活；
	// shell 的 echo $! 也会写一份，两份 PID 应一致，Java 以 shell
	// 那份为准、这份为备）。写 pid 文件在 Start 之前，Java 侧
	// 起进程后立刻就能读到，不用等节点上线。
	_ = os.WriteFile(*stateDir+"/gsic-tsnet.pid", []byte(fmt.Sprint(os.Getpid())+"\n"), 0644)
	// v3.85 修：无 key 时恒走登录等待，不再以“状态目录有非空文件”判断已登录。
	// 旧判断会挡链接：错密钥跑过一次就会在状态目录留下 tailscaled.state，
	// 下次无 key 启动就被判“有历史”走直接 Listen 分支，而该分支完全不轮询
	// AuthURL（tsnet 自己的交互登录链接只走 Logf，本进程按防泄漏又把它丢弃），
	// Java 层永远拿不到 TSNET_LOGIN_URL。改后：无 key 一律先 Start 再轮询
	// Status——已登录过的节点 Status 直接就是 Running，waitForLogin 立即回 IP，
	// 不出链接、不多等；从未登录成的节点则正常出链接。
	loginMode := key == ""

	// v3.93 测试：Android 上 Go 纯解析器读不到 /etc/resolv.conf 时
	// 回落到 [::1]:53，官方 VPN 在跑时该地址被拒绝，控制面取密钥
	// 超时、登录链接 hasUrl=false、卡面永远正在拉取；系统 curl 走
	// Android netd 解析却能通。改后强制用公共 DNS 做解析，依次
	// 试 8.8.8.8、223.5.5.5、1.1.1.1，任一通即用；若 Tailscale
	// 内部不用 DefaultResolver，此改无效，测试会直接证明。
	net.DefaultResolver = &net.Resolver{
		PreferGo: true,
		Dial: func(ctx context.Context, network, address string) (net.Conn, error) {
			d := net.Dialer{Timeout: 5 * time.Second}
			for _, dnsServer := range []string{"8.8.8.8:53", "223.5.5.5:53", "1.1.1.1:53"} {
				if c, err := d.DialContext(ctx, "udp", dnsServer); err == nil {
					return c, nil
				}
			}
			return d.DialContext(ctx, "udp", "8.8.8.8:53")
		},
	}
	// v3.87：登录等待期的共享 reporter（Status 轮询与 logf 回调两路共用）。
	rep := &loginReporter{}
	srv := &tsnet.Server{
		Hostname: *hostname,
		Dir:      *stateDir,
		AuthKey:  key,
		Logf: func(format string, args ...any) {
			msg := fmt.Sprintf(format, args...)
			// 后备链接来源：tsnet 自己的 printAuthURLLoop 在 NeedsLogin 时
			// 每 5 秒打一行 "go to: <链接>"；在 Go 内解析出链接、经行协议
			// 上报（与 Status 轮询同一去重），原始 logf 行不透传、不进
			// stderr——链接只走 TSNET_LOGIN_URL 行给 Java 层卡面展示。
			if idx := strings.Index(msg, "go to: "); idx >= 0 {
				if u := strings.TrimSpace(msg[idx+len("go to: "):]); strings.HasPrefix(u, "https://") {
					rep.reportURL(u)
				}
				return
			}
			// 其余内部行：只提炼脱敏类别进等待状态，不透传原文。
			rep.setActivity(classifyActivity(msg))
			fmt.Fprintln(os.Stderr, "TSNET_LOG "+msg)
		},
	}
	var closeOnce sync.Once
	closeAll := func() { closeOnce.Do(func() { srv.Close() }) }
	defer closeAll()

	// 停止信号先于登录等待建立：等链接、等授权期间用户点停止也能立即退出。
	// 两个触发源（stdin STOP 与系统信号）共用一次 close，避免重复 close panic。
	done := make(chan struct{})
	var doneOnce sync.Once
	closeDone := func() { doneOnce.Do(func() { close(done) }) }
	go func() {
		sc := bufio.NewScanner(os.Stdin)
		for sc.Scan() {
			if strings.TrimSpace(sc.Text()) == "STOP" {
				closeDone()
				return
			}
		}
		// v3.92：stdin 关闭不再视为停止。旧逻辑在 Java 侧输出流被
		// 回收/关闭时就让节点静默退出，卡面只剩重连循环、链接永远
		// 等不到；显式停止仍走 STOP 行或信号，Java 停进程时写 STOP
		// 后 800ms 未退出还会 destroy，EOF 不再是退出路径。
		// 此 goroutine 在 EOF 后直接返回，主流程继续等授权/信号。
	}()
	sigc := make(chan os.Signal, 2)
	signal.Notify(sigc, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigc
		closeDone()
	}()

	var ln net.Listener
	tailIP := ""
	if loginMode {
		// 网页登录模式：先起节点、轮询等授权，Running 后再 Listen。
		if err := srv.Start(); err != nil {
			fmt.Fprintln(os.Stderr, "TSNET_ERROR=start: "+sanitizeOneLine(err.Error()))
			emit("TSNET_ERROR=start: " + sanitizeOneLine(err.Error()))
			return 1
		}
		lc, lerr := srv.LocalClient()
		if lerr != nil {
			fmt.Fprintln(os.Stderr, "TSNET_ERROR=localclient: "+sanitizeOneLine(lerr.Error()))
			emit("TSNET_ERROR=localclient: " + sanitizeOneLine(lerr.Error()))
			return 1
		}
		ip, ok := waitForLogin(lc, done, rep)
		if !ok {
			// 等待期间被 STOP/信号打断：优雅退出，不算错误。
			closeAll()
			return 0
		}
		tailIP = ip
		var err error
		ln, err = srv.Listen("tcp", fmt.Sprintf(":%d", *listenPort))
		if err != nil {
			fmt.Fprintln(os.Stderr, "TSNET_ERROR=listen: "+sanitizeOneLine(err.Error()))
			emit("TSNET_ERROR=listen: " + sanitizeOneLine(err.Error()))
			return 1
		}
	} else {
		// 旧路径（有 key 或有历史状态）：直接 Listen，行为与 v3.82 一致。
		var err error
		ln, err = srv.Listen("tcp", fmt.Sprintf(":%d", *listenPort))
		if err != nil {
			fmt.Fprintln(os.Stderr, "TSNET_ERROR=listen: "+sanitizeOneLine(err.Error()))
			emit("TSNET_ERROR=listen: " + sanitizeOneLine(err.Error()))
			return 1
		}
		if ta, ok := ln.Addr().(*net.TCPAddr); ok && ta.IP != nil {
			tailIP = ta.IP.String()
		}
		if tailIP == "" {
			// 兜底：从节点状态里取第一个 tailnet IP（1.94 的 LocalClient 返回两个值）。
			if lc, lerr := srv.LocalClient(); lerr == nil {
				if st, err := lc.Status(context.Background()); err == nil && st != nil && st.Self != nil {
					for _, ip := range st.Self.TailscaleIPs {
						tailIP = ip.String()
						break
					}
				}
			}
		}
	}
	defer ln.Close()

	emit("TSNET_IP=" + tailIP)
	emit("TSNET_READY")
	fmt.Fprintln(os.Stderr, "gsic-tsnet listening on tailnet, forwarding to "+*forward)

	go func() {
		<-done
		ln.Close()
		closeAll()
	}()

	for {
		c, err := ln.Accept()
		if err != nil {
			select {
			case <-done:
				return 0
			default:
			}
			// 监听器已关之外的接受错误：记一笔继续，不退出进程。
			fmt.Fprintln(os.Stderr, "TSNET_LOG accept error: "+err.Error())
			time.Sleep(200 * time.Millisecond)
			continue
		}
		go proxyConn(c, *forward)
	}
}

// waitForLogin：网页登录等待循环（仅无 key 时调用）。
// 每 2 秒查一次 LocalClient Status（每轮新建 context.Background()，不用会
// 提前取消的短 ctx；轮询间隔 2 秒不变；收到 done 即返回，不拖）：
// NeedsLogin 且 AuthURL 非空时经 reporter 上报 TSNET_LOGIN_URL（链接变了
// 再报一次新的）；上次报出的链接被控制面收回、或同一链接 15 分钟未完成
// 授权时，报一次 TSNET_LOGIN_EXPIRED；BackendState 到 Running 且拿到
// tailnet IP 即返回（true）。等待期间每轮附带报一行 TSNET_STATUS=<后端
// 状态>|<脱敏类别>，供 Java 层卡面展示，替代干巴巴的“正在拉取”。
// 等待期间收到 done（STOP/信号）返回（"", false），调用方优雅退出。
// 注意：链接与过期提示只走 stdout 行协议，stderr 里绝不出现链接原文。
func waitForLogin(lc *local.Client, done chan struct{}, rep *loginReporter) (string, bool) {
	for {
		select {
		case <-done:
			return "", false
		default:
		}
		st, err := lc.Status(context.Background())
		if err == nil && st != nil {
			if st.BackendState == ipn.Running.String() {
				for _, ip := range st.TailscaleIPs {
					if ip.IsValid() {
						return ip.String(), true
					}
				}
				// Running 但 IP 还没下来：继续等一轮。
			} else {
				rep.reportURL(st.AuthURL)
				rep.checkExpired(st.AuthURL)
				act := rep.getActivity()
				if act == "" {
					act = "等待控制面响应"
				}
				emit("TSNET_STATUS=" + st.BackendState + "|" + act)
			}
		} else if err != nil {
			rep.setActivity(classifyActivity(err.Error()))
			act := rep.getActivity()
			if act == "" {
				act = "等待控制面响应"
			}
			emit("TSNET_STATUS=未知|" + act)
			fmt.Fprintln(os.Stderr, "TSNET_LOG login poll status error: "+err.Error())
		}
		select {
		case <-done:
			return "", false
		case <-time.After(2 * time.Second):
		}
	}
}

// hasState 已随 v3.85 移除：无 key 是否走登录等待不再看目录文件，
// 改看实时 BackendState（见 loginMode），残留状态文件不再挡链接。

// proxyConn：tailnet 连接与本机远控服务之间的双向原样转发。
func proxyConn(remote net.Conn, forward string) {
	defer remote.Close()
	local, err := net.DialTimeout("tcp", forward, 5*time.Second)
	if err != nil {
		fmt.Fprintln(os.Stderr, "TSNET_LOG dial local failed: "+err.Error())
		return
	}
	defer local.Close()
	done := make(chan struct{}, 2)
	go func() {
		io.Copy(local, remote)
		done <- struct{}{}
	}()
	go func() {
		io.Copy(remote, local)
		done <- struct{}{}
	}()
	<-done
}
