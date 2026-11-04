package com.gsic.app;

import android.content.Context;
import android.content.SharedPreferences;
import java.io.BufferedReader;
import java.io.InputStream;
import java.io.InputStreamReader;
import java.io.OutputStream;
import java.net.HttpURLConnection;
import java.net.URL;
import java.nio.charset.StandardCharsets;
import org.json.JSONObject;

/**
 * v3.78: 远控服务器模式——手机反向连 Muse 服务器（出站长连接）。
 * 背景（2026-10-30 用户选定）：旧远控是手机做服务器、Muse 做客户端，
 * 手机在 NAT 后，Muse 得靠 Tailscale/Cloudflare 拨进来，一断就全断。
 * 反向模式反过来：手机主动出站长轮询连中继服务器，命令走这条出站
 * 连接下发，手机不用开 inbound 端口，NAT/防火墙不用改。
 * 协议（与 ~/workspace/muse-relay-server.py 对齐）：
 *   GET  {relay}/poll?token=...   长轮询 30 秒，无命令回 {"command":null}
 *   POST {relay}/result           {"id":..., "ok":..., "result":...}
 *   命令：{"id":..., "type":"ping"|"status"|"task", "goal":..., "maxSteps":30}
 * 安全：token 只存 GSIC 本地 prefs（relay），只发往用户填的中继地址；
 * 中继地址生产上必须是 https，http 仅限局域网测试，UI 里已写明；
 * 命令可操控手机，中继必须带 token 鉴权，无 token 不启动。
 * 任务执行复用 TgBot 同款 AgentLoop 路径（setRemote(true)、
 * setChannel("relay")、TaskLock("relay") 三通道互斥），不是另起一套。
 * 本版不做 WebSocket（Android 纯 Java 无内置 WebSocket 客户端，
 * 不引第三方库），长轮询 30 秒已够命令下发，断线后 3 秒重连。
 * 调用方必须在后台线程调用 start()/stop()，状态经 Listener 回 UI。
 */
public class MuseRelayClient {

    public interface Listener {
        void onLog(String s);
        void onStatus(String s);
    }

    private final Context ctx;
    private final Listener listener;
    private volatile boolean running = false;
    private volatile Thread thread = null;
    private volatile String lastStatus = "未启动";

    public MuseRelayClient(Context ctx, Listener listener) {
        this.ctx = ctx.getApplicationContext();
        this.listener = listener;
    }

    public static SharedPreferences prefs(Context ctx) {
        return ctx.getSharedPreferences("relay", Context.MODE_PRIVATE);
    }

    public static String getUrl(Context ctx) {
        return prefs(ctx).getString("url", "");
    }

    public static String getToken(Context ctx) {
        return prefs(ctx).getString("token", "");
    }

    public static void save(Context ctx, String url, String token) {
        prefs(ctx).edit()
                .putString("url", url == null ? "" : url.trim())
                .putString("token", token == null ? "" : token.trim())
                .apply();
    }

    public static boolean hasConfig(Context ctx) {
        return !getUrl(ctx).isEmpty() && !getToken(ctx).isEmpty();
    }

    public boolean isRunning() {
        return running;
    }

    public String status() {
        return lastStatus;
    }

    public synchronized void start() {
        if (running) return;
        if (!hasConfig(ctx)) {
            log("未配置中继地址或 token，先在卡里填好再启动");
            return;
        }
        running = true;
        thread = new Thread(this::loop, "muse-relay");
        thread.start();
        lastStatus = "反向连接运行中（长轮询中继）";
        if (listener != null) listener.onStatus(lastStatus);
        log("Muse 服务器模式已启动：手机出站连中继，不用等拨入");
    }

    public synchronized void stop() {
        running = false;
        if (thread != null) thread.interrupt();
        lastStatus = "已停止";
        if (listener != null) listener.onStatus(lastStatus);
        log("Muse 服务器模式已停止");
    }

    private void log(String s) {
        if (listener != null) listener.onLog(s);
    }

    private void loop() {
        String base = getUrl(ctx);
        while (base.endsWith("/")) base = base.substring(0, base.length() - 1);
        String token = getToken(ctx);
        int failStreak = 0;
        while (running) {
            try {
                String body = httpGet(base + "/poll?token=" + encode(token), 10000, 35000);
                failStreak = 0;
                if (body == null || body.isEmpty()) continue;
                JSONObject resp = new JSONObject(body);
                if (resp.isNull("command")) continue;
                JSONObject cmd = resp.getJSONObject("command");
                String id = cmd.optString("id", "");
                String type = cmd.optString("type", "");
                lastStatus = "收到命令：" + type + "（" + id + "）";
                if (listener != null) listener.onStatus(lastStatus);
                handleCommand(base, token, cmd, id, type);
            } catch (InterruptedException e) {
                Thread.currentThread().interrupt();
                break;
            } catch (Exception e) {
                if (!running) break;
                failStreak++;
                if (failStreak >= 3) log("中继轮询异常（连续 " + failStreak + " 次）：" + e.getMessage());
                sleepQuiet(3000);
            }
        }
        running = false;
    }

    private void handleCommand(String base, String token, JSONObject cmd, String id, String type) {
        try {
            if (type.equals("ping")) {
                postResult(base, token, id, true, "pong");
                return;
            }
            if (type.equals("status")) {
                JSONObject st = new JSONObject();
                st.put("service", true);
                st.put("terms_accepted", TermsGate.isAccepted(ctx));
                st.put("relay", true);
                postResult(base, token, id, true, st.toString());
                return;
            }
            if (type.equals("task")) {
                String goal = cmd.optString("goal", "");
                int maxSteps = cmd.optInt("maxSteps", 30);
                if (goal.isEmpty()) {
                    postResult(base, token, id, false, "goal 为空");
                    return;
                }
                if (!TaskLock.tryAcquire("relay")) {
                    postResult(base, token, id, false, "任务忙：" + TaskLock.current() + " 通道正在执行");
                    return;
                }
                final String fBase = base;
                final String fToken = token;
                final String fId = id;
                new Thread(() -> {
                    StringBuilder logs = new StringBuilder();
                    try {
                        LlmStore llmStore = new LlmStore(ctx);
                        if (!llmStore.hasKey()) {
                            postResult(fBase, fToken, fId, false, "先在 App 大模型配置里填好 API Key");
                            return;
                        }
                        AgentLoop loop = new AgentLoop(ctx, llmStore.client(),
                                new AgentLoop.Listener() {
                                    @Override
                                    public void onLog(String msg) {
                                        logs.append(msg).append("\n");
                                    }

                                    @Override
                                    public boolean isStopped() {
                                        return !running;
                                    }
                                });
                        loop.setRemote(true);
                        loop.setChannel("relay");
                        boolean ok = loop.run(goal, maxSteps);
                        String tail = logs.toString();
                        if (tail.length() > 4000) tail = tail.substring(tail.length() - 4000);
                        postResult(fBase, fToken, fId, ok, tail);
                    } catch (Exception e) {
                        postResult(fBase, fToken, fId, false, "执行异常：" + e.getMessage());
                    } finally {
                        TaskLock.release("relay");
                    }
                }, "relay-task").start();
                return;
            }
            postResult(base, token, id, false, "未知命令类型：" + type);
        } catch (Exception e) {
            postResult(base, token, id, false, "处理异常：" + e.getMessage());
        }
    }

    private void postResult(String base, String token, String id, boolean ok, String result) {
        try {
            JSONObject o = new JSONObject();
            o.put("id", id);
            o.put("ok", ok);
            o.put("result", result == null ? "" : result);
            httpPost(base + "/result?token=" + encode(token), o.toString());
        } catch (Exception e) {
            log("回结果失败：" + e.getMessage());
        }
    }

    private static String encode(String s) {
        try {
            return java.net.URLEncoder.encode(s, "UTF-8");
        } catch (Exception e) {
            return s;
        }
    }

    private static void sleepQuiet(long ms) {
        try {
            Thread.sleep(ms);
        } catch (InterruptedException e) {
            Thread.currentThread().interrupt();
        }
    }

    private static String httpGet(String urlStr, int connectMs, int readMs) throws Exception {
        HttpURLConnection c = null;
        try {
            URL u = new URL(urlStr);
            c = (HttpURLConnection) u.openConnection();
            c.setConnectTimeout(connectMs);
            c.setReadTimeout(readMs);
            c.setRequestMethod("GET");
            InputStream in = c.getInputStream();
            BufferedReader br = new BufferedReader(new InputStreamReader(in, StandardCharsets.UTF_8));
            StringBuilder sb = new StringBuilder();
            String line;
            while ((line = br.readLine()) != null) sb.append(line);
            br.close();
            return sb.toString();
        } finally {
            if (c != null) c.disconnect();
        }
    }

    private static void httpPost(String urlStr, String json) throws Exception {
        HttpURLConnection c = null;
        try {
            URL u = new URL(urlStr);
            c = (HttpURLConnection) u.openConnection();
            c.setConnectTimeout(10000);
            c.setReadTimeout(15000);
            c.setRequestMethod("POST");
            c.setDoOutput(true);
            c.setRequestProperty("Content-Type", "application/json; charset=UTF-8");
            byte[] data = json.getBytes(StandardCharsets.UTF_8);
            OutputStream os = c.getOutputStream();
            os.write(data);
            os.flush();
            os.close();
            InputStream in;
            try {
                in = c.getInputStream();
            } catch (Exception e) {
                in = c.getErrorStream();
            }
            if (in != null) {
                while (in.read() != -1) {
                }
                in.close();
            }
        } finally {
            if (c != null) c.disconnect();
        }
    }
}
