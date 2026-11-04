package com.gsic.app;

import android.content.Context;
import android.content.SharedPreferences;
import java.io.BufferedReader;
import java.io.ByteArrayOutputStream;
import java.io.File;
import java.io.FileInputStream;
import java.io.FileOutputStream;
import java.io.InputStream;
import java.io.InputStreamReader;
import java.io.OutputStream;
import java.security.MessageDigest;

/**
 * v3.82：内置 Tailscale 节点（Go 小进程模型，照 cloudflared 内置的同一套路）。
 * 原理：Go 小进程（assets/gsic-tsnet，用 tsnet 库自己写的原创程序）加入用户
 * 的 tailnet，在 tailnet 上监听，把拨入流量原样转发到本机回环的远控端口；
 * AgentHttpServer 本来就只放行回环与 Tailscale 网段，所以转发进来的请求
 * 仍要过 X-Token 鉴权。tailnet 只换传输层，不降低鉴权，节点本身不是鉴权。
 * 目标：摆脱外部 Tailscale App 与 MIUI 后台启动开关，远控不再依赖它们活着。
 * 密钥：auth key 只存本机私有 prefs（tsnet），绝不进日志、不进 /status、
 * 不进命令行参数（走环境变量 TSNET_AUTHKEY 传给小进程）；节点长期状态
 * 存在 noBackupFilesDir/tsnet-state，避免备份恢复后冒充另一台设备。
 * 首登需要 auth key（Tailscale 后台生成），登录成功后状态落盘，下次
 * 启动不用重填；auth key 框里只显示掩码，保存时掩码原文不覆盖旧 key。
 * v3.84 网页登录：没有 auth key 也能点卡面“网页登录”，小进程无 key 启动
 * 后从控制面取授权链接（TSNET_LOGIN_URL 行协议），卡面展示链接、点开
 * 浏览器授权本设备，授权完成节点自动上线、不用再点启动；链接只在 App 内
 * 卡面展示，绝不进日志、不进 /status、不走聊天远控（与 auth key 掩码同级）；
 * 链接过期时卡面提示重拉（重启进程取新链接）；已登录态点网页登录直接
 * 显示已登录与当前 IP，不重复拉链接。
 * v3.88 自动跳转：协议行到达即通知弱引用监听者（MainActivity 在 resumed
 * 时注册、paused 时注销），链接首次到达且 Activity 在前台、tsnet 卡可见
 * 时自动跳浏览器打开（同一链接只跳一次，清空/过期后 guard 复位），
 * 不在前台或卡不可见时绝不后台拉起；手动按钮全部保留做后备。
 * v3.89 死因可见 + 启动竞态：进程非正常退出时卡面显示退出码与最近原始
 * 输出尾行（脱敏后），不再是空的“上次错误”；启动护栏保证同一时刻只有
 * 一个启动流程（连点排队不丢弃）；停旧起新时等到旧进程真正退出（上限
 * 3 秒）再起新进程，不撞状态目录锁；Go 侧致命错误与 panic 统一经
 * TSNET_ERROR= 上报。
 * 失败看门狗与 CloudflareTunnel 同款退避：5 / 10 / 20 / 40 / 60 秒封顶，
 * 用户点停止或服务销毁停进程时即停，不连发重试风暴。
 * v4.09 Go 进程根治（用户 2026-11-03 亲令“修复 go 进程”）：
 * 3.94 的 setsid 没根治——幽灵进程机制按 App UID（不是会话/进程组）
 * 认子进程，Go 进程只要还是 App 直接 fork 出来的、UID 还是 App 的，
 * 换了会话也照样在 App 切后台约 5 秒后被杀（logcat PhantomProcessRecord
 * died）；root 起的测试进程能活，正因为 UID 是 root、不在猎杀范围。
 * 而红米 Android 11 根本没有幽灵进程机制（Android 12+ 才有），所以
 * 红米走旧路一直能活、真我 Android 13 必死——两台表现不一致的根因在此。
 * 根治：Android 12+（SDK>=31）且有 root 时，不再用 ProcessBuilder
 * 直接 fork，改经常驻 root shell 起 Go 进程（UID=root、父进程是 su
 * shell，幽灵进程机制根本不看它）；通信随之从 stdout 管道改成协议文件
 * + pid 文件 + 信号（管道只对直接子进程存在，守护进程没有管道可读）。
 * 密钥绝不进 root shell 命令串：Java 把密钥写进状态目录 authkey 文件
 * （仅属主可读），Go 侧读文件；网页登录模式起进程前删掉该文件，防
 * 旧密钥残留把网页登录又带回密钥路径（v3.85 同款坑）。Android 12 以下
 * 或无 root 时原样走 ProcessBuilder + setsid 旧路，红米工作路径不动。
 */
public class TsnetNode {

    private static volatile Process proc = null;
    private static volatile boolean running = false;
    private static volatile String tailIp = "";
    private static volatile String lastError = "";
    private static volatile long startedAt = 0;
    private static volatile boolean autoReconnect = false;
    private static volatile int reconnectCount = 0;
    private static volatile int lastReconnectDelaySec = 0;
    private static volatile OutputStream procStdin = null;
    // 进程代际：每次新起进程或停进程时 +1，旧线程的重连循环见代际变了就退出，
    // 防止“停旧进程起新进程”后旧线程又拉起第二个进程抢 stdout 与监听。
    private static volatile int generation = 0;
    // 网页登录态：链接只在 App 内卡面展示，绝不进日志与 /status。
    private static volatile String loginUrl = "";
    private static volatile boolean loginWaiting = false;
    private static volatile boolean loginExpired = false;
    // 等待网页授权期间的控制面状态行（Go 的 TSNET_STATUS 行，脱敏类别词，
    // 如 NeedsLogin|正在注册）；卡面展示用，不进 /status、不进日志。
    private static volatile String loginStatus = "";
    // v3.89 死因可见：进程非正常退出时记退出码与最近原始输出尾行。
    private static volatile int lastExitCode = -1;
    private static volatile boolean runHadErrorLine = false;
    private static final java.util.ArrayDeque<String> rawTail = new java.util.ArrayDeque<String>();
    // v4.09 root 守护模式：Go 进程不再是 Java 的直接子进程，proc 字段
    // 无效，存活、停止、协议全部走 pid 文件 + 协议文件 + root shell 信号。
    private static volatile boolean daemonMode = false;
    private static volatile int daemonPid = -1;
    // v3.89 启动护栏：同一时刻只有一个启动流程（进入即置位、线程真正起跑
    // 或失败即复位）；护栏挡住的请求排队（pendingStart），由起跑/失败的
    // 线程接力，不丢弃。
    private static volatile boolean starting = false;
    private static volatile boolean pendingStart = false;
    private static volatile int pendingPort = 0;
    private static volatile boolean pendingWebLogin = false;

    /** UI 通知：协议行导致可见状态变化时通知监听者（不带任何敏感值，
     * 监听者自行读 getLoginUrl()/isLoginWaiting()/getLoginStatus() 等公开态）。
     * v3.88：解决卡面 stale——旧实现里链接到达只改内存变量，没有任何通知
     * UI 的机制；点网页登录只预定 3 秒、10 秒两次刷新，链接若在 10 秒后才到，
     * 卡面永远不更新，“在浏览器打开”按钮永远出不来，用户侧就是“不能跳转”。
     * 监听者用弱引用持有，Activity 在 resumed 时注册、paused 时注销，
     * 杜绝 Activity 泄漏；单个监听者抛异常不影响其他监听者。 */
    public interface TsnetListener {
        void onTsnetChanged();
    }

    private static final java.util.List<java.lang.ref.WeakReference<TsnetListener>> listeners =
            new java.util.ArrayList<java.lang.ref.WeakReference<TsnetListener>>();

    public static void addListener(TsnetListener l) {
        if (l == null) {
            return;
        }
        synchronized (listeners) {
            for (java.lang.ref.WeakReference<TsnetListener> r : listeners) {
                if (r.get() == l) {
                    return;
                }
            }
            listeners.add(new java.lang.ref.WeakReference<TsnetListener>(l));
        }
    }

    public static void removeListener(TsnetListener l) {
        if (l == null) {
            return;
        }
        synchronized (listeners) {
            java.util.Iterator<java.lang.ref.WeakReference<TsnetListener>> it = listeners.iterator();
            while (it.hasNext()) {
                TsnetListener cur = it.next().get();
                if (cur == null || cur == l) {
                    it.remove();
                }
            }
        }
    }

    private static void notifyListeners() {
        java.util.List<TsnetListener> snap = new java.util.ArrayList<TsnetListener>();
        synchronized (listeners) {
            java.util.Iterator<java.lang.ref.WeakReference<TsnetListener>> it = listeners.iterator();
            while (it.hasNext()) {
                TsnetListener cur = it.next().get();
                if (cur == null) {
                    it.remove();
                } else {
                    snap.add(cur);
                }
            }
        }
        for (TsnetListener cur : snap) {
            try {
                cur.onTsnetChanged();
            } catch (Exception ignored) {
            }
        }
    }

    public static boolean isRunning() {
        if (daemonMode) {
            return running && daemonPid > 0;
        }
        return running && proc != null && proc.isAlive();
    }

    /** 未运行时不返回旧 IP，卡面与 /status 不会拿到过期地址。 */
    public static String getTailIp() {
        return isRunning() ? tailIp : "";
    }

    public static String getError() {
        return lastError;
    }

    public static long getStartedAt() {
        return startedAt;
    }

    public static int getReconnectCount() {
        return reconnectCount;
    }

    public static boolean isAutoReconnect() {
        return autoReconnect;
    }

    /** 等待网页授权时才返回链接；其余任何时候返回空，调用方不得缓存外传。 */
    public static String getLoginUrl() {
        return (loginWaiting && !loginExpired) ? loginUrl : "";
    }

    /** 等待网页授权期间最新一条控制面状态行（脱敏类别词），非等待期返回空。 */
    public static String getLoginStatus() {
        return loginWaiting ? loginStatus : "";
    }

    /** v3.89：最近一次非正常退出的进程退出码（-1 表示未记录）。 */
    public static int getLastExitCode() {
        return lastExitCode;
    }

    /** v3.89：最近原始输出尾行（脱敏后、最多 3 行，以 " | " 连接）。 */
    public static String getRawTailText() {
        synchronized (rawTail) {
            StringBuilder sb = new StringBuilder();
            for (String s : rawTail) {
                if (sb.length() > 0) {
                    sb.append(" | ");
                }
                sb.append(s);
            }
            return sb.toString();
        }
    }

    public static boolean isLoginWaiting() {
        return loginWaiting && isRunning();
    }

    public static boolean isLoginExpired() {
        return loginExpired && isRunning();
    }

    /** 退避：第 1 次 5 秒，第 2 次 10 秒，第 3 次 20 秒，第 4 次 40 秒，之后封顶 60 秒。 */
    public static int nextBackoffSec(int attempt) {
        if (attempt <= 1) return 5;
        if (attempt == 2) return 10;
        if (attempt == 3) return 20;
        if (attempt == 4) return 40;
        return 60;
    }

    private static SharedPreferences prefs(Context ctx) {
        return ctx.getSharedPreferences("tsnet", Context.MODE_PRIVATE);
    }

    public static boolean isEnabled(Context ctx) {
        try {
            return prefs(ctx).getBoolean("enabled", false);
        } catch (Exception e) {
            return false;
        }
    }

    public static void setEnabled(Context ctx, boolean on) {
        try {
            prefs(ctx).edit().putBoolean("enabled", on).apply();
        } catch (Exception ignored) {
        }
    }

    /** auth key 明文只在私有 prefs 里，调用方绝不能把它打进日志或 /status。 */
    public static String getAuthKey(Context ctx) {
        try {
            String s = prefs(ctx).getString("authkey", "");
            return s == null ? "" : s;
        } catch (Exception e) {
            return "";
        }
    }

    public static void setAuthKey(Context ctx, String key) {
        try {
            prefs(ctx).edit().putString("authkey", key == null ? "" : key).apply();
        } catch (Exception ignored) {
        }
    }

    /** 清除已保存的 auth key（只清 prefs 里的密钥，不删登录状态目录）：
     * 已登录过的节点不受影响；无登录状态时清完再点网页登录会正常出链接。 */
    public static void clearAuthKey(Context ctx) {
        try {
            prefs(ctx).edit().remove("authkey").apply();
        } catch (Exception ignored) {
        }
    }

    /** 掩码：只露末 4 位；未填写时返回空，卡面自行写“未填写”。 */
    public static String maskAuthKey(String key) {
        if (key == null || key.isEmpty()) {
            return "";
        }
        if (key.length() <= 8) {
            return "****";
        }
        return "****" + key.substring(key.length() - 4);
    }

    /** 输入框里的文本是不是掩码原文（是则保存时不覆盖旧 key）。 */
    public static boolean isMaskedText(Context ctx, String text) {
        if (text == null) {
            return false;
        }
        String m = maskAuthKey(getAuthKey(ctx));
        return !m.isEmpty() && m.equals(text);
    }

    /** 节点状态目录：noBackupFilesDir 防备份恢复冒充，拿不到时回退 filesDir。 */
    public static File getStateDir(Context ctx) {
        try {
            File nb = ctx.getNoBackupFilesDir();
            if (nb != null) {
                return new File(nb, "tsnet-state");
            }
        } catch (Exception ignored) {
        }
        return new File(ctx.getFilesDir(), "tsnet-state");
    }

    /** 已有历史登录状态时，无 key 也能复用登录。
     * v3.92 修：旧实现把状态目录里任何非空文件都算历史状态，但目录里
     * 常年有 tailscaled.log*.txt / tailscaled.log.conf 这类日志文件，
     * 从未登录成的节点也会被误判成“有历史”，导致服务启动恢复走密钥
     * 路径而不是网页登录路径；改后只认非日志文件，日志与 logs 子目录
     * 一律不算。 */
    public static boolean hasState(Context ctx) {
        try {
            File d = getStateDir(ctx);
            File[] fs = d.listFiles();
            if (fs == null) {
                return false;
            }
            for (File f : fs) {
                if (!f.isFile() || f.length() <= 0) {
                    continue;
                }
                String n = f.getName();
                if (n.startsWith("tailscaled.log") || n.equals("tailscaled.log.conf")) {
                    continue;
                }
                return true;
            }
        } catch (Exception ignored) {
        }
        return false;
    }

    private static boolean hasKeyOrState(Context ctx) {
        return !getAuthKey(ctx).isEmpty() || hasState(ctx);
    }

    // ---- v4.09 root 守护路径 ----

    /** 只有 Android 12+ 才有幽灵进程机制，12 以下走旧路不动。 */
    private static boolean useRootDaemon() {
        try {
            if (android.os.Build.VERSION.SDK_INT < 31) {
                return false;
            }
            return Root.findSu() != null;
        } catch (Exception e) {
            return false;
        }
    }

    /** 经 root shell 同步跑一条短命令；返回 [exitCode, output]。 */
    private static String[] rootExec(String cmd, int timeoutSec) {
        try {
            return RootShell.get().exec(cmd, timeoutSec);
        } catch (Exception e) {
            return new String[]{"-1", ""};
        }
    }

    private static boolean pidAlive(int pid) {
        if (pid <= 0) {
            return false;
        }
        try {
            String[] r = rootExec("kill -0 " + pid, 5);
            return r != null && "0".equals(r[0]);
        } catch (Exception e) {
            return false;
        }
    }

    private static int readPidFile(File f) {
        try {
            if (f == null || !f.exists()) {
                return -1;
            }
            FileInputStream in = new FileInputStream(f);
            byte[] buf = new byte[64];
            int n = in.read(buf);
            in.close();
            if (n <= 0) {
                return -1;
            }
            return Integer.parseInt(new String(buf, 0, n, "UTF-8").trim());
        } catch (Exception e) {
            return -1;
        }
    }

    /** 守护进程的协议文件：stdout+stderr 都重定向到这里，Java 轮询读。 */
    private static File protoFile(Context ctx) {
        return new File(getStateDir(ctx), "protocol.log");
    }

    /** 密钥文件：仅属主可读写；网页登录模式起进程前必须删掉（防旧密钥残留）。 */
    private static File authKeyFile(Context ctx) {
        return new File(getStateDir(ctx), "authkey");
    }

    private static void writeAuthKeyFile(Context ctx, String key) {
        try {
            File f = authKeyFile(ctx);
            FileOutputStream out = new FileOutputStream(f);
            try {
                out.write(key.getBytes("UTF-8"));
            } finally {
                try { out.close(); } catch (Exception ignored) {}
            }
            // 仅属主可读写；root 进程可读，App 外其他 UID 读不到。
            f.setReadable(false, false);
            f.setWritable(false, false);
            f.setReadable(true, true);
            f.setWritable(true, true);
        } catch (Exception ignored) {
        }
    }

    private static void deleteAuthKeyFile(Context ctx) {
        try {
            File f = authKeyFile(ctx);
            if (f.exists()) {
                f.delete();
            }
        } catch (Exception ignored) {
        }
    }

    /** 认领已在跑的守护进程（服务重启、App 进程被杀后守护进程还活着时）：
     * pid 文件里的 pid 还活着、且 cmdline 里确实是 gsic-tsnet，才认领，
     * 否则当作过期 pid 文件删掉、由调用方重新起。认领成功返回 pid。 */
    private static int adoptDaemon(Context ctx) {
        try {
            File stateDir = getStateDir(ctx);
            int pid = readPidFile(new File(stateDir, "daemon.pid"));
            if (pid <= 0) {
                pid = readPidFile(new File(stateDir, "gsic-tsnet.pid"));
            }
            if (pid <= 0 || !pidAlive(pid)) {
                return -1;
            }
            String[] r = rootExec("cat /proc/" + pid + "/cmdline 2>/dev/null | tr '\\0' ' '", 5);
            String cmdline = (r != null && r.length > 1) ? r[1] : "";
            if (cmdline == null || !cmdline.contains("gsic-tsnet")) {
                return -1;
            }
            return pid;
        } catch (Exception e) {
            return -1;
        }
    }

    /** 只把协议行喂给 handleProtocolLine；TSNET_LOG 等内部日志不喂，
     * 不污染卡面死因的原始输出尾行。 */
    private static boolean isProtocolLine(String line) {
        return line != null && (line.startsWith("TSNET_IP=")
                || line.startsWith("TSNET_READY")
                || line.startsWith("TSNET_LOGIN_URL=")
                || line.startsWith("TSNET_LOGIN_EXPIRED")
                || line.startsWith("TSNET_STATUS=")
                || line.startsWith("TSNET_ERROR="));
    }

    /** 守护进程监控循环：轮询协议文件 + 每约 5 秒查一次 pid 存活；
     * 进程死了返回 false（调用方按退避重连），代际变了/开关关了返回 true（退出）。 */
    private static boolean monitorDaemon(Context ctx, int myGeneration) {
        File pf = protoFile(ctx);
        long offset = 0;
        int tick = 0;
        while (true) {
            if (myGeneration != generation || !isEnabled(ctx) || !autoReconnect) {
                return true;
            }
            try {
                if (pf.exists()) {
                    long len = pf.length();
                    if (len < offset) {
                        offset = 0;
                    }
                    if (len > offset) {
                        FileInputStream in = new FileInputStream(pf);
                        try {
                            long skipped = 0;
                            while (skipped < offset) {
                                long s = in.skip(offset - skipped);
                                if (s <= 0) {
                                    break;
                                }
                                skipped += s;
                            }
                            byte[] rest = readAll(in);
                            if (rest.length > 0) {
                                offset += rest.length;
                                String text = new String(rest, "UTF-8");
                                for (String line : text.split("\n")) {
                                    String l = line.trim();
                                    if (isProtocolLine(l)) {
                                        handleProtocolLine(l);
                                    }
                                }
                            }
                        } finally {
                            try { in.close(); } catch (Exception ignored) {}
                        }
                    }
                }
            } catch (Exception ignored) {
            }
            tick++;
            if (tick % 10 == 0) {
                // 约 5 秒查一次存活：守护进程被杀时这里先发现，不靠卡面猜。
                if (!pidAlive(daemonPid)) {
                    return false;
                }
            }
            try { Thread.sleep(500); } catch (Exception ignored) {}
        }
    }

    /** root 守护主循环：与 ProcessBuilder 循环同款退避与代际护栏，
     * 只是起停与协议全部走守护路径。 */
    private static void runDaemonLoop(final Context appCtx, final int port,
            final boolean webLogin, final int myGeneration) {
        while (true) {
            runHadErrorLine = false;
            lastExitCode = -1;
            synchronized (rawTail) { rawTail.clear(); }
            boolean startedThisRun = false;
            boolean delegated = false;
            try {
                File bin = ensureBinary(appCtx);
                File stateDir = getStateDir(appCtx);
                if (!stateDir.exists()) {
                    stateDir.mkdirs();
                }
                File logsDir = new File(stateDir, "logs");
                if (!logsDir.exists()) {
                    logsDir.mkdirs();
                }
                // 密钥模式写密钥文件、网页登录模式删密钥文件（防残留带偏模式）。
                if (webLogin) {
                    deleteAuthKeyFile(appCtx);
                } else {
                    String k = getAuthKey(appCtx);
                    if (k != null && !k.isEmpty()) {
                        writeAuthKeyFile(appCtx, k);
                    }
                }
                int pid = adoptDaemon(appCtx);
                if (pid > 0) {
                    daemonMode = true;
                    daemonPid = pid;
                    running = true;
                    starting = false;
                    pendingStart = false;
                    startedAt = System.currentTimeMillis();
                    startedThisRun = true;
                    // 认领后从协议文件头重放：IP/链接/状态按最后一条落定，
                    // 卡面不靠猜，直接恢复到守护进程的真实态。
                    boolean done = monitorDaemon(appCtx, myGeneration);
                    if (done) {
                        return;
                    }
                } else {
                    // 新起：shell 重定向截断协议文件，旧 pid 文件清掉防认领过期 pid。
                    File pf = protoFile(appCtx);
                    File daemonPidFile = new File(stateDir, "daemon.pid");
                    // 命令串里绝不能出现密钥：密钥只在 authkey 文件里，Go 侧读文件。
                    // 路径无空格，不用引号；< /dev/null 断开 stdin，Go 的 stdin
                    // 扫描 goroutine 立即 EOF 返回（v3.92 起 EOF 不再是退出路径）。
                    String cmd = bin.getAbsolutePath()
                            + " -dir " + stateDir.getAbsolutePath()
                            + " -forward 127.0.0.1:" + port
                            + " -port " + port
                            + " -hostname gsic"
                            + " > " + pf.getAbsolutePath() + " 2>&1 < /dev/null"
                            + " & echo $! > " + daemonPidFile.getAbsolutePath();
                    String[] r = rootExec(cmd, 10);
                    if (r == null || !"0".equals(r[0])) {
                        throw new Exception("root shell 起守护进程失败");
                    }
                    // 给 Go 一点时间写 pid 文件与协议文件头。
                    for (int i = 0; i < 20; i++) {
                        pid = readPidFile(daemonPidFile);
                        if (pid <= 0) {
                            pid = readPidFile(new File(stateDir, "gsic-tsnet.pid"));
                        }
                        if (pid > 0 && pidAlive(pid)) {
                            break;
                        }
                        try { Thread.sleep(100); } catch (Exception ignored) {}
                    }
                    if (pid <= 0 || !pidAlive(pid)) {
                        throw new Exception("守护进程起后 pid 不可用");
                    }
                    if (myGeneration != generation || !isEnabled(appCtx) || !autoReconnect) {
                        try { rootExec("kill -TERM " + pid, 5); } catch (Exception ignored) {}
                        daemonMode = false;
                        daemonPid = -1;
                        starting = false;
                        if (pendingStart) {
                            pendingStart = false;
                            int pp = pendingPort;
                            boolean pw = pendingWebLogin;
                            startInternalMode(appCtx, pp, pw);
                        }
                        return;
                    }
                    daemonMode = true;
                    daemonPid = pid;
                    running = true;
                    starting = false;
                    pendingStart = false;
                    startedAt = System.currentTimeMillis();
                    startedThisRun = true;
                    boolean done = monitorDaemon(appCtx, myGeneration);
                    if (done) {
                        return;
                    }
                }
            } catch (Exception e) {
                starting = false;
                if (pendingStart) {
                    pendingStart = false;
                    int pp = pendingPort;
                    boolean pw = pendingWebLogin;
                    startInternalMode(appCtx, pp, pw);
                    delegated = true;
                } else {
                    String msg = e.getMessage();
                    lastError = msg == null ? "守护进程启动失败" : msg;
                }
            } finally {
                if (myGeneration == generation) {
                    running = false;
                    // 守护进程死了才清模式位；代际变了（新进程已接管）时不动。
                    if (!delegated) {
                        daemonMode = false;
                        daemonPid = -1;
                    }
                    tailIp = "";
                    loginWaiting = false;
                    loginExpired = false;
                    loginUrl = "";
                    loginStatus = "";
                }
            }
            if (delegated) {
                return;
            }
            if (startedThisRun && myGeneration == generation
                    && isEnabled(appCtx) && autoReconnect) {
                lastError = "Go 守护进程已退出，正在自动重连";
                notifyListeners();
            }
            if (!isEnabled(appCtx) || !autoReconnect || myGeneration != generation) {
                break;
            }
            reconnectCount++;
            lastReconnectDelaySec = nextBackoffSec(reconnectCount);
            boolean stopped = false;
            for (int i = 0; i < lastReconnectDelaySec; i++) {
                try { Thread.sleep(1000); } catch (Exception ignored) {}
                if (!isEnabled(appCtx) || !autoReconnect || myGeneration != generation) {
                    stopped = true;
                    break;
                }
            }
            if (stopped) {
                break;
            }
        }
    }

    public static String statusText(Context ctx) {
        // v3.87：先判过期态再判等待态（过期分支已复位 loginWaiting，此处双保险，
        // 过期后绝不再显示“正在拉取”）。
        if (isLoginExpired()) {
            return "Tailscale 内置节点等待网页授权…\n登录链接已过期，点卡面“重新获取登录链接”取新链接。";
        }
        if (isLoginWaiting()) {
            if (loginUrl == null || loginUrl.isEmpty()) {
                String st = loginStatus == null ? "" : loginStatus;
                return "Tailscale 内置节点等待网页授权…\n正在向控制面拉取登录链接…"
                        + (st.isEmpty() ? "" : "\n控制面状态：" + st);
            }
            return "Tailscale 内置节点等待网页授权…\n登录链接已生成，点卡面按钮在浏览器打开并授权本设备；"
                    + "授权完成自动上线，不用再点启动。链接只在 App 内卡面显示，不进日志与 /status。";
        }
        if (isRunning()) {
            StringBuilder sb = new StringBuilder();
            sb.append("Tailscale 内置节点运行中\ntailnet IP：")
                    .append(tailIp.isEmpty() ? "获取中…" : tailIp)
                    .append("\n转发 → 127.0.0.1:")
                    .append(AgentService.getPort(ctx))
                    .append("（X-Token 鉴权不变，tailnet 只换传输层）");
            if (reconnectCount > 0) {
                sb.append("\n（已自动重连 ").append(reconnectCount).append(" 次）");
            }
            return sb.toString();
        }
        if (isEnabled(ctx) && autoReconnect) {
            return "Tailscale 内置节点断了，正在自动重连（第 " + reconnectCount
                    + " 次，约 " + lastReconnectDelaySec + " 秒后重试）\n上次错误：" + lastError;
        }
        if (!lastError.isEmpty()) {
            return "Tailscale 内置节点未运行\n上次错误：" + lastError;
        }
        return "Tailscale 内置节点未运行（未开启）";
    }

    /** 把 assets 里的 gsic-tsnet 拷到私有目录并加执行权限；缺失时抛错。
     * v3.87 修：旧实现只要私有目录文件存在、>1MB、可执行就直接返回，从不
     * 比对新版——跑过 3.82/3.83 的手机私有目录里永远是旧的 key-only 二进制，
     * 之后装 3.84/3.85 也不会换上带登录链接轮询的新二进制，网页登录必然
     * “一直在拉取”。改后每次启动前比对 assets 与私有目录文件的长度 +
     * SHA-256，不一致就重拷；读不到 assets 时直接抛错，绝不静默用旧二进制。 */
    public static File ensureBinary(Context ctx) throws Exception {
        File bin = new File(ctx.getFilesDir(), "gsic-tsnet");
        byte[] assetBytes;
        try {
            InputStream in = ctx.getAssets().open("gsic-tsnet");
            try {
                assetBytes = readAll(in);
            } finally {
                try { in.close(); } catch (Exception ignored) {}
            }
        } catch (Exception e) {
            throw new Exception("读不到 assets 里的 gsic-tsnet，拒绝用旧二进制启动", e);
        }
        if (assetBytes.length < 1000000) {
            throw new Exception("assets 里的 gsic-tsnet 不完整");
        }
        String assetHash = sha256Hex(assetBytes);
        if (bin.exists() && bin.length() == assetBytes.length && bin.canExecute()
                && assetHash.equals(sha256Hex(bin))) {
            return bin;
        }
        File tmp = new File(ctx.getFilesDir(), "gsic-tsnet.tmp");
        FileOutputStream out = new FileOutputStream(tmp);
        try {
            out.write(assetBytes);
        } finally {
            try { out.close(); } catch (Exception ignored) {}
        }
        if (tmp.length() != assetBytes.length) {
            throw new Exception("gsic-tsnet 写入不完整");
        }
        if (bin.exists()) {
            bin.delete();
        }
        if (!tmp.renameTo(bin)) {
            // rename 失败时退回直接写目标文件（同目录，不跨文件系统）。
            FileOutputStream out2 = new FileOutputStream(bin);
            try {
                out2.write(assetBytes);
            } finally {
                try { out2.close(); } catch (Exception ignored) {}
            }
        }
        bin.setExecutable(true, false);
        if (!bin.exists() || bin.length() != assetBytes.length || !bin.canExecute()) {
            throw new Exception("gsic-tsnet 二进制缺失或不完整");
        }
        return bin;
    }

    private static byte[] readAll(InputStream in) throws Exception {
        ByteArrayOutputStream bos = new ByteArrayOutputStream();
        byte[] buf = new byte[8192];
        int n;
        while ((n = in.read(buf)) > 0) {
            bos.write(buf, 0, n);
        }
        return bos.toByteArray();
    }

    private static String sha256Hex(byte[] data) throws Exception {
        MessageDigest md = MessageDigest.getInstance("SHA-256");
        byte[] d = md.digest(data);
        StringBuilder sb = new StringBuilder();
        for (byte b : d) {
            sb.append(String.format("%02x", b & 0xff));
        }
        return sb.toString();
    }

    private static String sha256Hex(File f) throws Exception {
        FileInputStream in = new FileInputStream(f);
        try {
            return sha256Hex(readAll(in));
        } finally {
            try { in.close(); } catch (Exception ignored) {}
        }
    }

    /** 用户点启动（auth key 路径）：无 key 又无历史状态时拒绝，不把开关置 true，避免空转重连。
     * 没有 key 时请点卡面“网页登录”，走浏览器授权。 */
    public static synchronized void start(final Context ctx, final int port) {
        if (isRunning()) {
            return;
        }
        if (!hasKeyOrState(ctx)) {
            lastError = "先填 Tailscale auth key（在 Tailscale 后台 Access Controls → Auth Keys 生成，"
                    + "建议只勾选可复用、不过期按需），或点卡面“网页登录”用浏览器授权；"
                    + "首登成功后状态存本机，下次不用重填。";
            return;
        }
        startInternal(ctx, port);
    }

    /** 用户点网页登录：恒走网页登录模式，不再因“有已保存密钥”回落到密钥路径
     * （v3.85 修：填错密钥后点网页登录曾永远走密钥路径、链接拉不到）。
     * 例外只有健康运行态（运行中且已有 tailnet IP），此时保持现状；
     * 运行中但无 IP（错密钥重试中、等授权中、出错态）时先停掉当前进程
     * 再进网页登录模式，不死在“已在运行”提示上。
     * 网页登录模式启动进程时一律不传 TSNET_AUTHKEY（见 startInternalMode）；
     * 有历史登录状态时 Go 靠 state 目录恢复节点、不出链接，这是对的。 */
    public static synchronized void startLogin(final Context ctx, final int port) {
        if (isRunning() && !tailIp.isEmpty()) {
            return;
        }
        stopProcessOnly();
        startLoginInternal(ctx, port);
    }

    /** 链接过期重拉：只杀进程不清开关，随即以无 key 模式重起取新链接。 */
    public static synchronized void refreshLoginUrl(final Context ctx, final int port) {
        if (isRunning() && !isLoginWaiting() && !isLoginExpired()) {
            return;
        }
        stopProcessOnly();
        startLoginInternal(ctx, port);
    }

    public static synchronized void startInternal(final Context ctx, final int port) {
        startInternalMode(ctx, port, false);
    }

    public static synchronized void startLoginInternal(final Context ctx, final int port) {
        startInternalMode(ctx, port, true);
    }

    private static synchronized void startInternalMode(final Context ctx, final int port, final boolean webLogin) {
        if (isRunning()) {
            return;
        }
        // v3.89 启动护栏：同一时刻只有一个启动流程；护栏挡住的请求排队
        // （极短窗口内连点），由起跑/失败的线程接力，不丢弃。
        if (starting) {
            pendingStart = true;
            pendingPort = port;
            pendingWebLogin = webLogin;
            return;
        }
        if (!webLogin && !hasKeyOrState(ctx)) {
            lastError = "先填 Tailscale auth key 才能启动内置节点，或点“网页登录”。";
            return;
        }
        starting = true;
        setEnabled(ctx, true);
        autoReconnect = true;
        generation++;
        final int myGeneration = generation;
        lastError = "";
        tailIp = "";
        reconnectCount = 0;
        final Context appCtx = ctx.getApplicationContext();
        // v3.85 修：网页登录模式一律不读已保存密钥、不传 TSNET_AUTHKEY，
        // 否则 Go 走密钥认证、永不出登录链接；密钥路径才读 key。
        final String authKey = webLogin ? "" : getAuthKey(appCtx);
        new Thread(new Runnable() {
            @Override
            public void run() {
                // v4.09：Android 12+ 且有 root 时走守护路径（UID=root，
                // 幽灵进程机制不杀）；其余设备原样走下面 ProcessBuilder 旧路。
                if (useRootDaemon()) {
                    runDaemonLoop(appCtx, port, webLogin, myGeneration);
                    return;
                }
                while (true) {
                    runHadErrorLine = false;
                    lastExitCode = -1;
                    synchronized (rawTail) { rawTail.clear(); }
                    int exitCode = -1;
                    boolean exited = false;
                    boolean restarted = false;
                    boolean startedThisRun = false;
                    try {
                        File bin = ensureBinary(appCtx);
                        File stateDir = getStateDir(appCtx);
                        if (!stateDir.exists()) {
                            stateDir.mkdirs();
                        }
                        // v3.90：logpolicy 兜底全灭修——生产进程 cwd=/ 且无 HOME/TMPDIR，
                        // 且手机无 /tmp，Go 侧 LogsDir 一路失败直至 panic。TS_LOGS_DIR
                        // 是 LogsDir 第一顺位，指向已建好的 App 私有目录即解决。
                        File logsDir = new File(stateDir, "logs");
                        if (!logsDir.exists()) {
                            logsDir.mkdirs();
                        }
                        java.util.List<String> args = new java.util.ArrayList<String>();
                        args.add(bin.getAbsolutePath());
                        args.add("-dir");
                        args.add(stateDir.getAbsolutePath());
                        args.add("-forward");
                        args.add("127.0.0.1:" + port);
                        args.add("-port");
                        args.add(String.valueOf(port));
                        args.add("-hostname");
                        args.add("gsic");
                        ProcessBuilder pb = new ProcessBuilder(args);
                        // key 只走环境变量，不进命令行参数（ps 可见），不进日志。
                        if (authKey != null && !authKey.isEmpty()) {
                            pb.environment().put("TSNET_AUTHKEY", authKey);
                        }
                        // v3.90：见上，TS_LOGS_DIR 第一顺位，App 私有目录已建好。
                        pb.environment().put("TS_LOGS_DIR", logsDir.getAbsolutePath());
                        pb.redirectErrorStream(true);
                        final Process myProc = pb.start();
                        proc = myProc;
                        try {
                            procStdin = myProc.getOutputStream();
                        } catch (Exception ignored) {
                        }
                        // v3.89：起跑后立刻复核——若此期间已有更新的停/启操作
                        // （代际变了、或开关已关），刚起的进程必须立即销毁，
                        // 不得复活成僵尸节点；排队的启动请求由它接力。
                        if (myGeneration != generation || !isEnabled(appCtx) || !autoReconnect) {
                            try { myProc.destroy(); } catch (Exception ignored) {}
                            proc = null;
                            procStdin = null;
                            starting = false;
                            if (pendingStart) {
                                pendingStart = false;
                                int pp = pendingPort;
                                boolean pw = pendingWebLogin;
                                startInternalMode(appCtx, pp, pw);
                            }
                            return;
                        }
                        running = true;
                        starting = false;
                        startedThisRun = true;
                        // 本线程已成功起跑：护栏期间的启动请求已被满足，清掉排队。
                        pendingStart = false;
                        startedAt = System.currentTimeMillis();
                        BufferedReader br = new BufferedReader(
                                new InputStreamReader(myProc.getInputStream(), "UTF-8"));
                        String line;
                        while ((line = br.readLine()) != null) {
                            handleProtocolLine(line);
                        }
                        try {
                            exitCode = myProc.waitFor();
                            exited = true;
                        } catch (Exception ignored) {
                        }
                    } catch (Exception e) {
                        starting = false;
                        if (pendingStart) {
                            // 本线程起跑失败，但有排队的启动请求：交给它（新模式），
                            // 本线程不再按旧模式重连，避免两种模式打架。
                            pendingStart = false;
                            int pp = pendingPort;
                            boolean pw = pendingWebLogin;
                            startInternalMode(appCtx, pp, pw);
                            restarted = true;
                        } else {
                            String msg = e.getMessage();
                            lastError = msg == null ? "启动失败" : msg;
                        }
                    } finally {
                        // 只有本代线程才清状态：停旧起新时旧线程的 finally 不得
                        // 覆盖新进程的 running/proc/tailIp（代际已变即跳过）。
                        if (myGeneration == generation) {
                            running = false;
                            proc = null;
                            procStdin = null;
                            tailIp = "";
                            // 进程没了，旧链接即失效，不留给卡面展示。
                            loginWaiting = false;
                            loginExpired = false;
                            loginUrl = "";
                            loginStatus = "";
                        }
                    }
                    if (restarted) {
                        break;
                    }
                    // v3.89 死因可见：非用户主动停止的退出（仍在重连态），卡面
                    // 必须看到退出码与原始输出尾行，不再是空的“上次错误”。
                    if (myGeneration == generation && isEnabled(appCtx) && autoReconnect) {
                        if (startedThisRun) {
                            lastExitCode = exitCode;
                            StringBuilder cause = new StringBuilder();
                            if (runHadErrorLine && !lastError.isEmpty()) {
                                cause.append(lastError);
                                if (exited) {
                                    cause.append("（进程退出码 ").append(exitCode).append("）");
                                }
                            } else if (exited) {
                                cause.append("进程退出码 ").append(exitCode);
                                String tail = getRawTailText();
                                if (tail.isEmpty()) {
                                    cause.append("（无输出）");
                                } else {
                                    cause.append("；原始输出尾行：").append(tail);
                                }
                            } else {
                                // waitFor 未返回（被中断等）：保留 catch 记下的原始异常信息，
                                // 不要用通用文案覆盖它；进程未起跑的失败（startedThisRun
                                // 为假）根本不进这里，catch 的原始信息原样保留。
                                if (!lastError.isEmpty()) {
                                    cause.append(lastError);
                                }
                                cause.append("（进程异常结束，退出码未知）");
                                String tail = getRawTailText();
                                if (!tail.isEmpty()) {
                                    cause.append("；原始输出尾行：").append(tail);
                                }
                            }
                            String c = cause.toString();
                            lastError = c.length() > 500 ? c.substring(0, 500) : c;
                        }
                        notifyListeners();
                    }
                    if (!isEnabled(appCtx) || !autoReconnect || myGeneration != generation) {
                        break;
                    }
                    reconnectCount++;
                    lastReconnectDelaySec = nextBackoffSec(reconnectCount);
                    boolean stopped = false;
                    for (int i = 0; i < lastReconnectDelaySec; i++) {
                        try { Thread.sleep(1000); } catch (Exception ignored) {}
                        if (!isEnabled(appCtx) || !autoReconnect || myGeneration != generation) {
                            stopped = true;
                            break;
                        }
                    }
                    if (stopped) {
                        break;
                    }
                }
            }
        }, "tsnet-node").start();
    }

    /** v3.89 脱敏：原始输出行含链接/密钥嫌疑的一律丢弃，绝不存、不显。
     * 链接只进内存变量、不进日志、不进 /status 的既有规则不变。 */
    static String sanitizeRawLine(String line) {
        if (line == null) {
            return null;
        }
        String low = line.toLowerCase(java.util.Locale.ROOT);
        if (low.contains("go to:") || low.contains("tsnet_login_url=") || low.contains("tsnet_authkey")
                || low.contains("authkey") || low.contains("login.tailscale.com")
                || low.contains("http://") || low.contains("https://")) {
            return null;
        }
        return line.length() > 200 ? line.substring(0, 200) : line;
    }

    private static void recordRawLine(String line) {
        String s = sanitizeRawLine(line);
        if (s == null) {
            return;
        }
        synchronized (rawTail) {
            rawTail.addLast(s);
            while (rawTail.size() > 3) {
                rawTail.pollFirst();
            }
        }
    }

    /** Go 小进程 stdout 行协议的统一处理（抽出来便于单测验证状态变迁）。
     * TSNET_STATUS 行只存脱敏类别词；TSNET_LOGIN_EXPIRED 分支必须同时复位
     * loginWaiting（v3.87 修：旧实现只置 loginExpired、不复位 loginWaiting，
     * 卡面先判等待态就永远显示“正在拉取”、重拉按钮永远出不来）。
     * v3.88：任一可见状态变化的行处理完都通知监听者，卡面不再依赖
     * 3 秒/10 秒两次预定刷新；TSNET_LOG 等无意义行不通知。 */
    static void handleProtocolLine(String line) {
        boolean changed = false;
        if (line.startsWith("TSNET_IP=")) {
            tailIp = line.substring("TSNET_IP=".length());
            // 上线即完成授权：链接不再有效，立即清掉。
            loginWaiting = false;
            loginExpired = false;
            loginUrl = "";
            loginStatus = "";
            lastError = "";  // 上线成功：旧死因清掉，不留过期错误。
            changed = true;
        } else if (line.startsWith("TSNET_READY")) {
            loginWaiting = false;
            loginExpired = false;
            loginUrl = "";
            loginStatus = "";
            lastError = "";  // 上线成功：旧死因清掉。
            changed = true;
        } else if (line.startsWith("TSNET_LOGIN_URL=")) {
            // 链接只进内存变量、只供卡面展示，绝不打日志、不进 /status。
            loginUrl = line.substring("TSNET_LOGIN_URL=".length());
            loginWaiting = true;
            loginExpired = false;
            changed = true;
        } else if (line.startsWith("TSNET_LOGIN_EXPIRED")) {
            loginWaiting = false;
            loginExpired = true;
            loginUrl = "";
            loginStatus = "";
            changed = true;
        } else if (line.startsWith("TSNET_STATUS=")) {
            String st = line.substring("TSNET_STATUS=".length());
            loginStatus = st.length() > 200 ? st.substring(0, 200) : st;
            changed = true;
        } else if (line.startsWith("TSNET_ERROR=")) {
            // 小进程自身不打印 key 与链接，错误行可直接存。
            String m = line.substring("TSNET_ERROR=".length());
            lastError = m.length() > 200 ? m.substring(0, 200) : m;
            runHadErrorLine = true;
            changed = true;
        } else {
            // v3.89：未知行不再直接丢弃——Go 的 panic/致命输出（stderr 合并后）
            // 都走这里，脱敏后留最近 3 行供死因展示。
            if (line != null && !line.trim().isEmpty()) {
                recordRawLine(line);
            }
        }
        // TSNET_LOG 不用存，运行态由 running/tailIp 表达。
        if (changed) {
            notifyListeners();
        }
    }

    public static synchronized void stop(Context ctx) {
        setEnabled(ctx, false);
        autoReconnect = false;
        loginWaiting = false;
        loginExpired = false;
        loginUrl = "";
        loginStatus = "";
        stopProcessOnly();
        // 用户主动停：卡面立即清掉，不必等 500ms 预定刷新。
        notifyListeners();
    }

    /** 只杀进程、不清开关：服务销毁时用，下次服务启动按开关恢复。
     * 先写 STOP 让小进程优雅关监听与节点，800ms 内未退出才 destroy。 */
    public static synchronized void stopProcessOnly() {
        autoReconnect = false;
        pendingStart = false;
        generation++;
        // v4.09 守护路径：Go 已有 SIGTERM 优雅退出（信号处理器），
        // 停进程就是发 TERM、等 800ms、未退出再等 3 秒，与旧路同款等待。
        if (daemonMode && daemonPid > 0) {
            int pid = daemonPid;
            try {
                rootExec("kill -TERM " + pid, 5);
                long deadline = System.currentTimeMillis() + 800;
                while (pidAlive(pid) && System.currentTimeMillis() < deadline) {
                    try { Thread.sleep(50); } catch (Exception ignored) {}
                }
                long deadline2 = System.currentTimeMillis() + 3000;
                while (pidAlive(pid) && System.currentTimeMillis() < deadline2) {
                    try { Thread.sleep(50); } catch (Exception ignored) {}
                }
                if (pidAlive(pid)) {
                    lastError = "旧守护进程停止超时（3 秒未退出），新进程可能抢锁失败";
                }
            } catch (Exception ignored) {
            }
            daemonMode = false;
            daemonPid = -1;
            running = false;
            proc = null;
            procStdin = null;
            tailIp = "";
            loginWaiting = false;
            loginExpired = false;
            loginUrl = "";
            loginStatus = "";
            return;
        }
        try {
            if (proc != null) {
                try {
                    if (procStdin != null) {
                        procStdin.write("STOP\n".getBytes("UTF-8"));
                        procStdin.flush();
                    }
                } catch (Exception ignored) {
                }
                long deadline = System.currentTimeMillis() + 800;
                while (proc.isAlive() && System.currentTimeMillis() < deadline) {
                    try { Thread.sleep(50); } catch (Exception ignored) {}
                }
                if (proc.isAlive()) {
                    proc.destroy();
                    // v3.89：destroy 后继续等到进程真正退出（上限 3 秒），避免
                    // 新进程撞上还没死透的旧进程抢状态目录锁；超时才放弃并记
                    // 一条死因（纯停止场景下卡面可见；切换路径随后被新一轮
                    // 的起跑结果覆盖）。
                    long deadline2 = System.currentTimeMillis() + 3000;
                    while (proc.isAlive() && System.currentTimeMillis() < deadline2) {
                        try { Thread.sleep(50); } catch (Exception ignored) {}
                    }
                    if (proc.isAlive()) {
                        lastError = "旧进程停止超时（3 秒未退出），新进程可能抢锁失败";
                    }
                }
            }
        } catch (Exception ignored) {
        }
        running = false;
        proc = null;
        procStdin = null;
        tailIp = "";
        loginWaiting = false;
        loginExpired = false;
        loginUrl = "";
        loginStatus = "";
    }

    /** 服务启动时按开关恢复节点（AgentService 调用）。
     * 开关开着但既无 key 又无状态（上次还在等网页授权就被杀掉）时，
     * 恢复成网页登录等待而不是直接报“先填 key”。 */
    public static void startIfEnabled(Context ctx, int port) {
        if (isEnabled(ctx) && !isRunning()) {
            if (hasKeyOrState(ctx)) {
                startInternal(ctx, port);
            } else {
                startLoginInternal(ctx, port);
            }
        }
    }
}
