package com.gsic.app;

import android.content.Context;
import android.content.SharedPreferences;
import java.io.BufferedReader;
import java.io.File;
import java.io.FileOutputStream;
import java.io.InputStream;
import java.io.InputStreamReader;
import java.util.regex.Matcher;
import java.util.regex.Pattern;

/**
 * v3.67: cloudflared 内置隧道（快速隧道，临时测试用）。
 * 原理：手机主动出站连 Cloudflare 边缘，拿到一个随机的 trycloudflare.com 地址，
 * 该地址只转发到本机回环的远控端口；AgentHttpServer 本来就只放行回环，
 * 所以隧道进来的请求在服务端看来就是 127.0.0.1，仍要过 X-Token 鉴权。
 * 隧道本身不是鉴权：拿到地址的人没有 token 仍然进不去。
 * 快速隧道地址每次启动就换，本版先走手动同步：用户在 App 里看到地址后
 * 发给云端 Muse，云端再用该地址探 /status。固定域名是后续阶段。
 * 二进制：构建时从 Cloudflare 官方 GitHub releases 拉取 linux-arm64 静态版
 * 放进 assets（不进 git，build.sh 缺失时自动拉取，与 vdinput.dex 同款做法）。
 * 注意：Android 上直接 exec 静态 Go 二进制是否成功，须真机实测，
 * 失败时 lastError 会原样记录，UI 直接展示，不谎报已通。
 *
 * v3.80: 自动重连 + 换址提醒（用户亲选方向，不含自动同步到云端）：
 * 1）自动重连：进程退出后只要开关还开着（isEnabled），看门狗就在同一线程
 * 里按退避重拉：5 / 10 / 20 / 40 / 60 秒封顶，不忙轮询、不烧电；
 * 用户点停止（stop）或服务销毁停进程（stopProcessOnly）都会把
 * autoReconnect 关掉，重连立刻停，下次服务启动按开关恢复时再开。
 * 2）换址提醒：新地址与上次地址不同时记 prevUrl + urlChanged，持久化
 * 进 prefs（cf），进程被杀、App 重启都不丢；卡面与 /status 露出
 * 旧地址与 changed 标志，用户点“地址已同步，清除提醒”才清掉。
 * 提醒不等于自动同步：新地址仍需用户手动发给云端，自动同步到云端
 * 需要另设公开汇合点，是下一阶段，不在本版偷偷做。
 */
public class CloudflareTunnel {

    private static final Pattern URL_PAT =
            Pattern.compile("https://(?!api\\.)[A-Za-z0-9-]+\\.trycloudflare\\.com");

    private static volatile Process proc = null;
    private static volatile boolean running = false;
    private static volatile String url = "";
    private static volatile String lastError = "";
    private static volatile long startedAt = 0;
    private static volatile boolean useProxy = false;
    // v3.80
    private static volatile boolean autoReconnect = false;
    private static volatile int reconnectCount = 0;
    private static volatile int lastReconnectDelaySec = 0;
    private static volatile String prevUrl = "";
    private static volatile boolean urlChanged = false;

    public static boolean isRunning() {
        return running && proc != null && proc.isAlive();
    }

    public static String getUrl() {
        // v3.72：未运行时不返回旧地址，/status 与复制按钮都不会拿到过期/误报地址
        return isRunning() ? url : "";
    }

    public static String getError() {
        return lastError;
    }

    public static long getStartedAt() {
        return startedAt;
    }

    // v3.80：纯逻辑，可 JVM 单测，不碰 Android 运行时
    /** 退避：第 1 次 5 秒，第 2 次 10 秒，第 3 次 20 秒，第 4 次 40 秒，之后封顶 60 秒。 */
    public static int nextBackoffSec(int attempt) {
        if (attempt <= 1) return 5;
        if (attempt == 2) return 10;
        if (attempt == 3) return 20;
        if (attempt == 4) return 40;
        return 60;
    }

    /** 换址判定：旧地址非空、新地址非空、两者不同，才算换址（首次拿到地址不算）。 */
    public static boolean isUrlChange(String oldUrl, String newUrl) {
        return oldUrl != null && !oldUrl.isEmpty()
                && newUrl != null && !newUrl.isEmpty()
                && !oldUrl.equals(newUrl);
    }

    public static int getReconnectCount() {
        return reconnectCount;
    }

    public static boolean isUrlChanged() {
        return urlChanged;
    }

    public static String getPrevUrl() {
        return prevUrl == null ? "" : prevUrl;
    }

    public static boolean isAutoReconnect() {
        return autoReconnect;
    }

    private static SharedPreferences prefs(Context ctx) {
        return ctx.getSharedPreferences("cf", Context.MODE_PRIVATE);
    }

    private static void saveReminder(Context ctx) {
        try {
            android.content.SharedPreferences.Editor e = prefs(ctx).edit();
            // url 为空时（进程刚退出、重连等待中）绝不能把 last_url 清掉，
            // 下次新地址要靠它比对换址
            if (url != null && !url.isEmpty()) {
                e.putString("last_url", url);
            }
            e.putString("prev_url", prevUrl == null ? "" : prevUrl)
                    .putBoolean("url_changed", urlChanged)
                    .putInt("reconnects", reconnectCount)
                    .apply();
        } catch (Exception ignored) {
        }
    }

    private static void loadReminder(Context ctx) {
        try {
            SharedPreferences p = prefs(ctx);
            String lu = p.getString("last_url", "");
            prevUrl = p.getString("prev_url", "");
            urlChanged = p.getBoolean("url_changed", false);
            reconnectCount = p.getInt("reconnects", 0);
            // 本轮 url 尚未拿到，last_url 只作换址比对用，不直接当成当前地址
            if (lu == null) lu = "";
        } catch (Exception ignored) {
        }
    }

    private static String persistedLastUrl(Context ctx) {
        try {
            String s = prefs(ctx).getString("last_url", "");
            return s == null ? "" : s;
        } catch (Exception e) {
            return "";
        }
    }

    /** 用户已把新地址发给云端，清掉换址提醒；旧地址记为当前地址，下次再变才提醒。 */
    public static void clearUrlChanged(Context ctx) {
        urlChanged = false;
        if (url != null && !url.isEmpty()) {
            prevUrl = url;
        }
        try {
            prefs(ctx).edit()
                    .putBoolean("url_changed", false)
                    .putString("prev_url", prevUrl == null ? "" : prevUrl)
                    .apply();
        } catch (Exception ignored) {
        }
    }

    public static boolean isProxyMode(Context ctx) {
        try {
            return ctx.getSharedPreferences("cf", Context.MODE_PRIVATE)
                    .getBoolean("proxy", false);
        } catch (Exception e) {
            return false;
        }
    }

    public static void setProxyMode(Context ctx, boolean on) {
        try {
            ctx.getSharedPreferences("cf", Context.MODE_PRIVATE)
                    .edit().putBoolean("proxy", on).apply();
        } catch (Exception ignored) {
        }
    }

    public static boolean isEnabled(Context ctx) {
        try {
            return ctx.getSharedPreferences("cf", Context.MODE_PRIVATE)
                    .getBoolean("enabled", false);
        } catch (Exception e) {
            return false;
        }
    }

    public static void setEnabled(Context ctx, boolean on) {
        try {
            ctx.getSharedPreferences("cf", Context.MODE_PRIVATE)
                    .edit().putBoolean("enabled", on).apply();
        } catch (Exception ignored) {
        }
    }

    public static String statusText(Context ctx) {
        if (isRunning()) {
            String u = url.isEmpty() ? "启动中，等待 Cloudflare 分配地址…" : url;
            StringBuilder sb = new StringBuilder();
            sb.append("Cloudflare 隧道运行中（").append(useProxy ? "走代理 7890 + http2" : "直连").append("）\n").append(u);
            if (urlChanged && prevUrl != null && !prevUrl.isEmpty()) {
                sb.append("\n地址已变！旧地址：").append(prevUrl)
                        .append("，请把新地址发给云端 Muse，云端用新地址探 /status；")
                        .append("发完点下面“地址已同步，清除提醒”。");
            }
            if (reconnectCount > 0) {
                sb.append("\n（已自动重连 ").append(reconnectCount).append(" 次）");
            }
            return sb.toString();
        }
        if (isEnabled(ctx) && autoReconnect) {
            return "Cloudflare 隧道断了，正在自动重连（第 " + reconnectCount
                    + " 次，约 " + lastReconnectDelaySec + " 秒后重试）\n上次错误：" + lastError;
        }
        if (!lastError.isEmpty()) {
            return "Cloudflare 隧道未运行\n上次错误：" + lastError;
        }
        return "Cloudflare 隧道未运行（未开启）";
    }

    /** 把 assets 里的 cloudflared 拷到私有目录并加执行权限；缺失时抛错。 */
    public static File ensureBinary(Context ctx) throws Exception {
        File bin = new File(ctx.getFilesDir(), "cloudflared");
        if (bin.exists() && bin.length() > 1000000 && bin.canExecute()) {
            return bin;
        }
        InputStream in = null;
        FileOutputStream out = null;
        try {
            in = ctx.getAssets().open("cloudflared");
            out = new FileOutputStream(bin);
            byte[] buf = new byte[8192];
            int n;
            while ((n = in.read(buf)) > 0) {
                out.write(buf, 0, n);
            }
        } finally {
            if (out != null) {
                try { out.close(); } catch (Exception ignored) {}
            }
            if (in != null) {
                try { in.close(); } catch (Exception ignored) {}
            }
        }
        bin.setExecutable(true, false);
        if (!bin.exists() || bin.length() < 1000000) {
            throw new Exception("cloudflared 二进制缺失或不完整");
        }
        return bin;
    }

    /** 直连启动（保持 3.67 行为）。 */
    public static synchronized void start(final Context ctx, final int port) {
        startWithProxy(ctx, port, false);
    }

    /** 走代理启动：HTTPS_PROXY 指到本机 clash 7890，并强制 http2 走 TCP，
     * 避免默认 QUIC（UDP）被透明代理/防火墙拦住；本机回环走 NO_PROXY。 */
    public static synchronized void startWithProxy(final Context ctx, final int port, final boolean proxy) {
        if (isRunning()) {
            return;
        }
        setProxyMode(ctx, proxy);
        startInternal(ctx, port, proxy);
    }

    /** 在后台线程启动快速隧道；URL 从进程输出里解析，解析到之前 url 为空。
     * v3.80：同一线程内看门狗循环，进程退出后只要开关还开着就按退避重拉，
     * 用户停或服务销毁停进程时 autoReconnect 为 false，循环即退出。 */
    public static synchronized void startInternal(final Context ctx, final int port, final boolean proxy) {
        if (isRunning()) {
            return;
        }
        setEnabled(ctx, true);
        autoReconnect = true;
        useProxy = proxy;
        lastError = "";
        url = "";
        loadReminder(ctx);
        reconnectCount = 0;
        saveReminder(ctx);
        final Context appCtx = ctx.getApplicationContext();
        new Thread(new Runnable() {
            @Override
            public void run() {
                while (true) {
                    try {
                        File bin = ensureBinary(appCtx);
                        java.util.List<String> args = new java.util.ArrayList<String>();
                        args.add(bin.getAbsolutePath());
                        args.add("tunnel");
                        if (proxy) {
                            args.add("--protocol");
                            args.add("http2");
                        }
                        args.add("--url");
                        args.add("http://127.0.0.1:" + port);
                        args.add("--no-autoupdate");
                        ProcessBuilder pb = new ProcessBuilder(args);
                        if (proxy) {
                            pb.environment().put("HTTPS_PROXY", "http://127.0.0.1:7890");
                            pb.environment().put("HTTP_PROXY", "http://127.0.0.1:7890");
                            pb.environment().put("NO_PROXY", "127.0.0.1,localhost");
                        }
                        pb.redirectErrorStream(true);
                        proc = pb.start();
                        running = true;
                        startedAt = System.currentTimeMillis();
                        BufferedReader br = new BufferedReader(
                                new InputStreamReader(proc.getInputStream(), "UTF-8"));
                        String line;
                        while ((line = br.readLine()) != null) {
                            String low = line.toLowerCase();
                            // v3.72：报错行不解析地址（2026-10-29 实测：
                            // failed 行里的 https://api.trycloudflare.com/tunnel
                            // 曾被旧正则当成隧道地址，/status 误报）
                            if (low.contains("error") || low.contains("failed")
                                    || low.contains("err ")) {
                                lastError = line.length() > 200
                                        ? line.substring(0, 200) : line;
                                continue;
                            }
                            Matcher m = URL_PAT.matcher(line);
                            if (m.find()) {
                                String nu = m.group();
                                if (!nu.equals(url)) {
                                    if (isUrlChange(url, nu)) {
                                        prevUrl = url;
                                        urlChanged = true;
                                    } else if (url.isEmpty()) {
                                        String persisted = persistedLastUrl(appCtx);
                                        if (isUrlChange(persisted, nu)) {
                                            prevUrl = persisted;
                                            urlChanged = true;
                                        }
                                    }
                                    url = nu;
                                    saveReminder(appCtx);
                                }
                            }
                        }
                        try {
                            proc.waitFor();
                        } catch (Exception ignored) {
                        }
                    } catch (Exception e) {
                        String msg = e.getMessage();
                        lastError = msg == null ? "启动失败" : msg;
                    } finally {
                        running = false;
                        proc = null;
                        // v3.72：进程退出后地址即失效，清掉，免得 /status 还报旧地址；
                        // v3.80：清之前 last_url 已在解析时存进 prefs，下次比对用它
                        url = "";
                    }
                    // 看门狗：开关关了或用户/服务停了进程，就退出，不重连
                    if (!isEnabled(appCtx) || !autoReconnect) {
                        break;
                    }
                    reconnectCount++;
                    lastReconnectDelaySec = nextBackoffSec(reconnectCount);
                    saveReminder(appCtx);
                    // 按秒分段睡，中间用户点了停止能立刻停，不用等满退避
                    boolean stopped = false;
                    for (int i = 0; i < lastReconnectDelaySec; i++) {
                        try { Thread.sleep(1000); } catch (Exception ignored) {}
                        if (!isEnabled(appCtx) || !autoReconnect) {
                            stopped = true;
                            break;
                        }
                    }
                    if (stopped) {
                        break;
                    }
                }
            }
        }, "cf-tunnel").start();
    }

    public static synchronized void stop(Context ctx) {
        setEnabled(ctx, false);
        autoReconnect = false;
        urlChanged = false;
        try {
            prefs(ctx).edit().putBoolean("url_changed", false).apply();
        } catch (Exception ignored) {
        }
        stopProcessOnly();
    }

    /** 只杀进程、不清开关：服务销毁时用，下次服务启动按开关恢复。
     * v3.80：同时关看门狗，免得服务销毁后看门狗又把隧道拉起来。 */
    public static synchronized void stopProcessOnly() {
        autoReconnect = false;
        try {
            if (proc != null) {
                proc.destroy();
            }
        } catch (Exception ignored) {
        }
        running = false;
        proc = null;
        url = "";
    }

    /** 服务启动时按开关恢复隧道（AgentService 调用）。 */
    public static void startIfEnabled(Context ctx, int port) {
        if (isEnabled(ctx) && !isRunning()) {
            startInternal(ctx, port, isProxyMode(ctx));
        }
    }
}
