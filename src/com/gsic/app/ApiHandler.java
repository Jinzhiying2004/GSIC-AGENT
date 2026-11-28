package com.gsic.app;

import android.content.Context;
import android.content.SharedPreferences;

import org.json.JSONArray;
import org.json.JSONObject;

import java.util.ArrayList;
import java.util.List;
import java.util.Map;
import java.util.UUID;

/**
 * HTTP API 路由 + token 鉴权。
 * token 首次启动时随机生成，存在 SharedPreferences，App 界面可见。
 */
public class ApiHandler implements AgentHttpServer.Handler {

    private final Context ctx;
    private final TokenManager tokenManager;
    private final PairingManager pairingManager;

    public ApiHandler(Context ctx) {
        this.ctx = ctx.getApplicationContext();
        // v3.11 Issue 1：Token 走 TokenManager（轮换/过期/设备绑定）
        this.tokenManager = new TokenManager(this.ctx);
        // Issue #3：一次性配对码 + 设备凭据
        this.pairingManager = new PairingManager(this.ctx);
    }

    public String getToken() {
        return tokenManager.getToken();
    }

    /** v3.11：吊销 token */
    public void revokeToken() {
        tokenManager.revoke();
    }

    /** v3.11：轮换 token */
    public String rotateToken() {
        return tokenManager.rotate();
    }

    // ---------- 远控目标式任务注册表（/task 异步 + /task/status 轮询 + App 内卡片共用） ----------
    private static final java.util.LinkedHashMap<String, RemoteTask> remoteTasks =
            new java.util.LinkedHashMap<>();

    // v4.23：当前正在跑的 steps 任务 Runner（/task/stop 用）
    private static volatile TaskRunner currentStepsRunner = null;

    public static synchronized void putRemoteTask(RemoteTask rt) {
        remoteTasks.put(rt.taskId, rt);
        while (remoteTasks.size() > 5) {
            String eldest = remoteTasks.keySet().iterator().next();
            remoteTasks.remove(eldest);
        }
    }

    public static synchronized RemoteTask getRemoteTask(String taskId) {
        return taskId == null ? null : remoteTasks.get(taskId);
    }

    /** 最近一个远控任务（App 内卡片 / 不带 taskId 的 status 查询用） */
    public static synchronized RemoteTask getLatestRemoteTask() {
        RemoteTask latest = null;
        for (RemoteTask rt : remoteTasks.values()) latest = rt;
        return latest;
    }

    // ---------- 远控任务悬浮窗：启动显示进度，结束留 5 秒后隐藏 ----------
    private static void floatShow(Context appCtx, String text) {
        // v3.39：走任务级登记（在 App 前台发起也不丢窗）
        FloatWindow.beginTask(appCtx, text);
    }

    private static void floatEnd(String text) {
        FloatWindow.endTask(text);
    }

    @Override
    public String handle(String method, String path, Map<String, String> query,
                         Map<String, String> headers, String body,
                         byte[] rawBody) throws Exception {
        // Issue #3：POST /pair 在鉴权前处理（用一次性配对码换设备凭据，
        // 配对本身就是取凭据的入口，不能先要求凭据）
        if ("/pair".equals(path)) {
            if (!"POST".equalsIgnoreCase(method)) {
                return new JSONObject().put("ok", false).put("error", "POST required").toString();
            }
            return pairingManager.handlePair(body).toString();
        }
        // Issue #1：只认 X-Token 请求头，不再接受 ?token= 查询参数（防凭据进 URL 日志）
        // Issue #3：主 Token 或有效设备 Token 都可鉴权（主 Token 保留，现有远控脚本继续能用）
        String tok = headers.get("x-token");
        // v3.11：token 走 TokenManager（支持轮换/过期）
        if (!tokenManager.getToken().equals(tok)
                && !pairingManager.isValidDeviceToken(tok)) {
            return new JSONObject().put("ok", false).put("error", "unauthorized").toString();
        }
        switch (path) {
            case "/status": {
                boolean firstCore = !TermsGate.isFirstRemoteTold(ctx);
                if (firstCore) TermsGate.markFirstRemoteTold(ctx);
                JSONObject st = new JSONObject()
                        .put("ok", true)
                        .put("version", 2)
                        .put("accessibility", AgentAccessibilityService.isRunning())
                        .put("service", AgentService.isRunning())
                        .put("terms_accepted", TermsGate.isAccepted(ctx))
                        .put("weekly_sediment_due", TermsGate.weeklyDue(ctx))
                        .put("core_first", firstCore)
                        .put("cf_tunnel_running", CloudflareTunnel.isRunning())
                        .put("cf_tunnel_url", CloudflareTunnel.getUrl())
                        .put("cf_tunnel_proxy", CloudflareTunnel.isProxyMode(ctx))
                        .put("cf_tunnel_url_changed", CloudflareTunnel.isUrlChanged())
                        .put("cf_tunnel_prev_url", CloudflareTunnel.getPrevUrl())
                        .put("cf_tunnel_reconnects", CloudflareTunnel.getReconnectCount())
                        .put("cf_tunnel_auto_reconnect", CloudflareTunnel.isAutoReconnect())
                        .put("tsnet_running", TsnetNode.isRunning())
                        .put("tsnet_ip", TsnetNode.getTailIp())
                        .put("tsnet_reconnects", TsnetNode.getReconnectCount())
                        .put("tsnet_auto_reconnect", TsnetNode.isAutoReconnect())
                        .put("tsnet_disconnect_restarts", TsnetNode.getDisconnectRestarts())
                        .put("tsnet_net_state", TsnetNode.getLastNetState())
                        .put("tsnet_last_ip_at", TsnetNode.getLastIpAt())
                        .put("heartbeat_ts", TsnetNode.getLastHeartbeatTs())
                        .put("heartbeat_ip", TsnetNode.getLastHeartbeatIp())
                        .put("node_uptime_sec", TsnetNode.getNodeUptimeSec());
                if (firstCore) st.put("core", TermsGate.CORE_TEXT);
                return st.toString();
            }
            case "/core":
                return new JSONObject()
                        .put("ok", true)
                        .put("name", "GSIC")
                        .put("core", TermsGate.CORE_TEXT)
                        .put("stage", "parasitic")
                        .put("compute_required", true)
                        .put("weekly_sediment_to", "GitHub Issues")
                        .put("terms_accepted", TermsGate.isAccepted(ctx))
                        .toString();
            case "/screen": {
                // v3.53：远控单步操控也是在干活——打活动心跳点亮彩条
                FloatWindow.pulseActivity(ctx);
                List<AgentAccessibilityService.NodeInfo> items =
                        AgentAccessibilityService.dumpScreen();
                JSONArray arr = new JSONArray();
                for (int idx = 0; idx < items.size(); idx++) {
                    AgentAccessibilityService.NodeInfo ni = items.get(idx);
                    arr.put(new JSONObject()
                            .put("id", idx)
                            .put("text", ni.text)
                            .put("desc", ni.desc)
                            .put("clickable", ni.clickable)
                            .put("cx", ni.cx)
                            .put("cy", ni.cy));
                }
                return new JSONObject().put("ok", true).put("nodes", arr).toString();
            }
            case "/action": {
                if (!TermsGate.isAccepted(ctx)) {
                    return new JSONObject().put("ok", false).put("error", "terms_not_accepted").toString();
                }
                if (!"POST".equalsIgnoreCase(method)) {
                    return new JSONObject().put("ok", false).put("error", "POST required").toString();
                }
                // v3.53：单步操控心跳（远控旁聊的主要干活方式，此前永不点亮彩条）
                FloatWindow.pulseActivity(ctx);
                List<String> logs = new ArrayList<>();
                TaskRunner r = new TaskRunner(ctx, logs::add);
                r.setRemote(true);
                boolean ok = r.execOneStep(body);
                return new JSONObject().put("ok", ok).put("log", new JSONArray(logs)).toString();
            }
            case "/task": {
                if (!TermsGate.isAccepted(ctx)) {
                    return new JSONObject().put("ok", false).put("error", "terms_not_accepted").toString();
                }
                if (!"POST".equalsIgnoreCase(method)) {
                    return new JSONObject().put("ok", false).put("error", "POST required").toString();
                }
                // v4.23：/task 只接受 steps 模式（goal 模式已移除，见上）
                JSONObject bj = null;
                try {
                    bj = new JSONObject(body);
                } catch (Exception ignored) {
                }
                if (bj != null && bj.has("goal") && !bj.has("steps")) {
                    // v4.23 简化重构：goal 模式已移除（手机端不再内置智能体），
                    // 云端请发送 steps 任务。
                    return new JSONObject().put("ok", false).put("error",
                            "goal 模式已移除，请发送 steps 任务").toString();
                }
                List<String> logs = new ArrayList<>();
                // v4.23：steps 任务登记到 RemoteTask 注册表，/task/status 可轮询
                // 进度，/task/stop 可远程停止（异步壳改跑 steps）。
                final RemoteTask rt = new RemoteTask("steps 任务", 0);
                putRemoteTask(rt);
                TaskRunner r = new TaskRunner(ctx, m -> {
                    logs.add(m);
                    rt.log(m);
                });
                r.setRemote(true);
                currentStepsRunner = r;
                // v4.23：远控高危策略偏好改由 ApiHandler 读（原 AgentLoop 在读，已删）
                try {
                    String rp = ctx.getSharedPreferences("agent", Context.MODE_PRIVATE)
                            .getString("remote_risk_policy", "deny");
                    r.setRemoteRiskPolicy(rp);
                } catch (Exception ignored) {
                }
                // v4.08 所有软件优先虚拟屏：步骤任务也一样——
                // 没虚拟屏时先建屏（task_vd_default 关了不建），
                // 建成了步骤里的 launch/tap/input 等自动改写为 vd_*；
                // 已有别的任务在用虚拟屏时不抢（步骤留在实体屏，
                // 显式 vd_* 步骤仍直通虚拟屏）；建屏失败回实体屏；
                // 本任务建的屏跑完即销毁，不留空虚拟屏占着主页。
                boolean createdVd = false;
                try {
                    boolean vdOn = ctx.getSharedPreferences("agent",
                            Context.MODE_PRIVATE)
                            .getBoolean("task_vd_default", true);
                    if (vdOn && !VirtualDisplayManager.isActive()) {
                        int did = VirtualDisplayManager.create(
                                ctx, 720, 1280, 320);
                        if (did >= 0) {
                            createdVd = true;
                            r.setVdMode(true);
                            logs.add("所有软件优先虚拟屏：已建虚拟屏并锁定");
                        } else {
                            // v4.14 补：建屏失败也是跳到实体屏跑，
                            // 结束同样回 GSIC 主页（与软件不支持同理）。
                            ctx.getSharedPreferences("agent",
                                    android.content.Context.MODE_PRIVATE)
                                    .edit().putBoolean("task_jumped_out", true).apply();
                        }
                    }
                } catch (Exception ignored) {}
                boolean ok = r.run(body);
                currentStepsRunner = null;
                rt.ok = ok;
                rt.done = true;
                try {
                    if (createdVd) VirtualDisplayManager.destroy();
                } catch (Exception ignored) {}
                // v4.14 补（用户 11-03 亲定）：步骤任务跳出后也回
                // GSIC 主页——下次打开的标签页记为主页，并把 GSIC
                // 拉到前台（横屏即横屏模式、竖屏即主页）。
                try {
                    boolean jumped = ctx.getSharedPreferences("agent",
                            android.content.Context.MODE_PRIVATE)
                            .getBoolean("task_jumped_out", false);
                    if (jumped) {
                        ctx.getSharedPreferences("agent",
                                android.content.Context.MODE_PRIVATE)
                                .edit().putBoolean("task_jumped_out", false).apply();
                        ctx.getSharedPreferences("ui",
                                android.content.Context.MODE_PRIVATE)
                                .edit().putInt("tab", 0).apply();
                        try {
                            android.content.Intent it = new android.content.Intent(
                                    ctx, MainActivity.class);
                            it.addFlags(android.content.Intent.FLAG_ACTIVITY_NEW_TASK
                                    | android.content.Intent.FLAG_ACTIVITY_CLEAR_TOP
                                    | android.content.Intent.FLAG_ACTIVITY_SINGLE_TOP);
                            ctx.startActivity(it);
                            logs.add("跳出任务结束，已回 GSIC 主页");
                        } catch (Exception e) {
                            logs.add("跳出任务结束回 GSIC 主页失败（下次打开仍为主页）");
                        }
                    }
                } catch (Exception ignored) {}
                return new JSONObject().put("ok", ok).put("log", new JSONArray(logs)).toString();
            }
            case "/task/status": {
                // 远控任务的实时进度（steps 任务）：执行日志边跑边看
                // ?taskId=xxx（不带则返回最近一个任务）
                if (!"GET".equalsIgnoreCase(method)) {
                    return new JSONObject().put("ok", false).put("error", "GET required").toString();
                }
                String tid = query.get("taskId");
                RemoteTask rt = (tid == null || tid.isEmpty())
                        ? getLatestRemoteTask() : getRemoteTask(tid);
                if (rt == null) {
                    return new JSONObject().put("ok", false)
                            .put("error", "无远控任务").toString();
                }
                return rt.toJson().toString();
            }
            case "/task/stop": {
                // v3.21：远程停止任务（v4.23 起停 steps 任务）
                // POST {"taskId":"rt-1-xxx"}，不带 taskId 则停最近一个
                if (!"POST".equalsIgnoreCase(method)) {
                    return new JSONObject().put("ok", false)
                            .put("error", "POST required").toString();
                }
                JSONObject bj = null;
                try {
                    bj = new JSONObject(body);
                } catch (Exception ignored) {
                }
                String tid = bj != null ? bj.optString("taskId", "") : "";
                RemoteTask rt = (tid == null || tid.isEmpty())
                        ? getLatestRemoteTask() : getRemoteTask(tid);
                if (rt == null) {
                    return new JSONObject().put("ok", false)
                            .put("error", "无远控任务").toString();
                }
                rt.stopRequested = true;
                rt.log("⏹ 收到远程停止请求");
                // v4.23：steps 任务在跑时直接停 TaskRunner
                TaskRunner cur = currentStepsRunner;
                if (cur != null) cur.stop();
                return new JSONObject().put("ok", true)
                        .put("stopped", true)
                        .put("taskId", rt.taskId).toString();
            }
            case "/patch": {
                // 热补丁推送：body 为 patch zip 二进制（rawBody，未经字符串转换）
                if (!"POST".equalsIgnoreCase(method)) {
                    return new JSONObject().put("ok", false).put("error", "POST required").toString();
                }
                PatchManager.PatchResult pr = PatchManager.stagePatch(ctx, rawBody);
                JSONObject r = new JSONObject().put("ok", pr.ok);
                if (!pr.ok) {
                    r.put("error", pr.error);
                } else {
                    r.put("tag", pr.tag);
                    // v3.22-hotfix：先回响应，再延迟重启使补丁生效
                    // （双保险：精确闹钟 + 杀进程靠服务拉起，MIUI 可靠）
                    final Context appCtx = ctx.getApplicationContext();
                    new Thread(() -> {
                        try {
                            Thread.sleep(1500);
                        } catch (InterruptedException ignored) {
                        }
                        PatchManager.scheduleRestart(appCtx, 800);
                        try {
                            Thread.sleep(400);
                        } catch (InterruptedException ignored) {
                        }
                        PatchManager.killForRestart();
                    }).start();
                }
                return r.toString();
            }
            case "/screenshot": {
                if (!"GET".equalsIgnoreCase(method)) {
                    return new JSONObject().put("ok", false).put("error", "GET required").toString();
                }
                // v3.53：操控中的截图同样算活动（远控旁聊靠它盯屏）
                FloatWindow.pulseActivity(ctx);
                try {
                    byte[] png = null;
                    String via = null;
                    // v3.15：?vd=1 截虚拟屏（云端驱动虚拟屏任务时用）
                    boolean wantVd = "1".equals(query.get("vd"));
                    if (wantVd && VirtualDisplayManager.isActive()) {
                        android.graphics.Bitmap vdb =
                                VirtualDisplayManager.screenshot();
                        if (vdb != null) {
                            java.io.ByteArrayOutputStream baos =
                                    new java.io.ByteArrayOutputStream();
                            vdb.compress(android.graphics.Bitmap.CompressFormat.PNG,
                                    100, baos);
                            png = baos.toByteArray();
                            vdb.recycle();
                            via = "virtual_display";
                        }
                    }
                    // 1) 无障碍截屏（Android 11+，无需 root）
                    android.graphics.Bitmap bmp = AgentAccessibilityService.screenshot();
                    if (bmp != null) {
                        java.io.ByteArrayOutputStream baos =
                                new java.io.ByteArrayOutputStream();
                        bmp.compress(android.graphics.Bitmap.CompressFormat.PNG,
                                100, baos);
                        png = baos.toByteArray();
                        bmp.recycle();
                        via = "accessibility";
                    }
                    // 2) 兜底：root 下 screencap
                    if (png == null) {
                        java.io.File f = new java.io.File(ctx.getCacheDir(), "shot.png");
                        if (f.exists()) f.delete();
                        Process p = Root.exec("screencap -p " + f.getAbsolutePath());
                        p.waitFor();
                        if (f.exists() && f.length() > 0) {
                            png = java.nio.file.Files.readAllBytes(f.toPath());
                            f.delete();
                            via = "root";
                        }
                    }
                    if (png == null) {
                        return new JSONObject().put("ok", false)
                                .put("error", "screenshot failed").toString();
                    }
                    // compact=1：降分辨率 + JPEG 压缩（给 LLM/远程看屏省 token 流量）
                    boolean compact = "1".equals(query.get("compact"));
                    if (compact) {
                        try {
                            android.graphics.Bitmap shotBmp =
                                    android.graphics.BitmapFactory.decodeByteArray(
                                            png, 0, png.length);
                            byte[] jpg = ScreenshotUtil.toJpegSmall(shotBmp, 720, 70);
                            if (shotBmp != null) shotBmp.recycle();
                            if (jpg != null) {
                                String b64 = android.util.Base64.encodeToString(
                                        jpg, android.util.Base64.NO_WRAP);
                                return new JSONObject().put("ok", true)
                                        .put("via", via).put("compact", true)
                                        .put("jpg", b64).toString();
                            }
                        } catch (Exception ignored) {
                        }
                    }
                    String b64 = android.util.Base64.encodeToString(
                            png, android.util.Base64.NO_WRAP);
                    return new JSONObject().put("ok", true)
                            .put("via", via).put("png", b64).toString();
                } catch (Exception e) {
                    return new JSONObject().put("ok", false)
                            .put("error", String.valueOf(e.getMessage())).toString();
                }
            }
            case "/agent-prompt": {
                // v4.33：连接智能体——与电脑版同款接口。扫码拿到地址 + Token 的
                // 智能体调它取完整操作说明全文。鉴权已在 handle() 入口统一做
                //（只认 X-Token 请求头）。
                if (!"GET".equalsIgnoreCase(method)) {
                    return new JSONObject().put("ok", false).put("error", "GET required").toString();
                }
                return new JSONObject().put("ok", true)
                        .put("prompt", AgentPrompt.fillPrompt(ctx)).toString();
            }
            default:
                return new JSONObject().put("ok", false).put("error", "unknown path").toString();
        }
    }
}
