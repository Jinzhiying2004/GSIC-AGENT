package com.gsic.app;

import android.content.Context;
import android.content.SharedPreferences;

import java.io.File;
import java.nio.charset.StandardCharsets;
import java.nio.file.Files;
import java.util.ArrayList;
import java.util.List;

/**
 * v3.83 第二批：统一设置注册表。
 * 新设置必须先在这里注册（path → 类型/默认值/校验/风险/人话描述），UI 只是渲染器；
 * 旧的各功能卡入口过渡期保留，设置页里以跳转行指向它们，不一次性删入口。
 * 硬规则：密钥类（模型 key、远控 token、tsnet auth key）绝不进注册表
 * 的值存储、不进审计日志、不进 /status；设置页里它们只以“去原卡修改”跳转行出现。
 */
public class SettingRegistry {

    public static final String PREFS = "gsic_settings";

    public enum Kind { BOOL, INT, STRING, INFO, JUMP }

    public static class Field {
        public String path;
        public String group;
        public String title;
        public String desc;
        public Kind kind;
        public String def = "";
        public int min = Integer.MIN_VALUE;
        public int max = Integer.MAX_VALUE;
        /** 危险项：写前必须弹确认（改 token、改路由、清数据类操作）。 */
        public boolean dangerous = false;
        /** JUMP 行的目标：远控分区 key，或 "agenttab"（智能体页）。 */
        public String jumpKey = "";
        public Field(String p, String g, String t, String d, Kind k) {
            path = p; group = g; title = t; desc = d; kind = k;
        }
    }

    public static final String G_UI = "外观与显示";
    public static final String G_CONN = "连接与远控";
    public static final String G_NET = "网络与代理";
    public static final String G_MODEL = "模型与 Provider";
    public static final String G_AGENT = "智能体行为";
    public static final String G_STORE = "存储与日志";
    public static final String G_ABOUT = "关于与安全";

    private static Field f(String p, String g, String t, String d, Kind k) {
        return new Field(p, g, t, d, k);
    }

    /** 全部注册字段（顺序即设置页渲染顺序）。 */
    public static List<Field> fields() {
        List<Field> out = new ArrayList<>();
        Field x;
        // v4.07 外观与显示：夜间模式默认开，章鱼待机时间可改
        x = f("ui.night_mode", G_UI, "夜间模式", "默认开启；关闭为日间模式，切换后界面重建生效", Kind.BOOL);
        x.def = "true"; out.add(x);
        x = f("ui.standby_seconds", G_UI, "章鱼待机时间（秒）", "横屏无操作多少秒后进章鱼待机（默认 1800 秒即 30 分钟；设置里可手动改任意秒数，保存后立即按新时间重算）", Kind.INT);
        x.def = "1800"; x.min = 1; x.max = Integer.MAX_VALUE; out.add(x);
        // v4.18 锁屏章鱼待机；v4.20 起默认关（用户 11-06 令）：关了则锁屏亮屏时不再挂黑屏章鱼
        x = f("ui.lock_standby", G_UI, "锁屏章鱼待机", "默认关闭；打开后开机自启后锁屏亮屏时也显示黑屏章鱼（仅显示、不挡解锁：滑动解锁、输密码、人脸解锁都穿透；任务开始、解锁、灭屏时自动收起；需悬浮窗权限）", Kind.BOOL);
        x.def = "false"; out.add(x);
        x = f("conn.remote_port", G_CONN, "远控端口", "只读：当前 HTTP 服务端口", Kind.INFO);
        out.add(x);
        x = f("conn.remote_service", G_CONN, "远程控制服务", "开关与端口在原卡", Kind.JUMP);
        x.jumpKey = "svc"; out.add(x);
        x = f("conn.cf_tunnel", G_CONN, "Cloudflare 隧道", "启停与换址提醒在原卡", Kind.JUMP);
        x.jumpKey = "cf"; out.add(x);
        x = f("conn.tsnet", G_CONN, "Tailscale 内置节点", "危险：auth key 修改在原卡（只显掩码）", Kind.JUMP);
        x.jumpKey = "tsnet"; x.dangerous = true; out.add(x);
        x = f("conn.relay", G_CONN, "Muse 服务器模式", "反向出站中继开关在原卡", Kind.JUMP);
        x.jumpKey = "relay"; out.add(x);
        x = f("model.main_model", G_MODEL, "主模型", "只读：当前主模型名", Kind.INFO);
        out.add(x);
        x = f("model.provider", G_MODEL, "模型与 Provider 配置", "Key 与 Provider 在智能体页配置", Kind.JUMP);
        x.jumpKey = "agenttab"; out.add(x);
        x = f("agent.chat_follow", G_AGENT, "对话跟随滚动", "流式输出时自动跟随到底；上滑即停跟随", Kind.BOOL);
        x.def = "true"; out.add(x);
        x = f("agent.loop_warn", G_AGENT, "防环警告阈值", "同一动作连续重复几次警告并换策略（1–10）", Kind.INT);
        x.def = "3"; x.min = 1; x.max = 10; out.add(x);
        x = f("agent.loop_fuse", G_AGENT, "防环熔断阈值", "同一动作连续重复几次熔断停任务（2–20）", Kind.INT);
        x.def = "5"; x.min = 2; x.max = 20; out.add(x);
        x = f("agent.skill_autodeprioritize", G_AGENT, "技能卡长期未用自动降权", "30 天未用的卡排到注入末尾（不删除）", Kind.BOOL);
        x.def = "true"; out.add(x);
        x = f("agent.skills", G_AGENT, "技能卡管理", "启用开关与本次会话覆盖在智能体页技能卡行", Kind.JUMP);
        x.jumpKey = "agenttab"; out.add(x);
        x = f("store.audit_enabled", G_STORE, "记录配置变更审计", "只记改了哪项与新旧摘要，密钥只记“已更改”", Kind.BOOL);
        x.def = "true"; out.add(x);
        x = f("about.version", G_ABOUT, "版本", "只读", Kind.INFO);
        out.add(x);
        x = f("about.terms", G_ABOUT, "用户协议状态", "只读：gsic_terms 接受状态", Kind.INFO);
        out.add(x);
        return out;
    }

    public static List<Field> fieldsOf(String group) {
        List<Field> out = new ArrayList<>();
        for (Field fld : fields()) {
            if (fld.group.equals(group)) out.add(fld);
        }
        return out;
    }

    public static String[] groups() {
        return new String[]{G_UI, G_CONN, G_NET, G_MODEL, G_AGENT, G_STORE, G_ABOUT};
    }

    private static SharedPreferences prefs(Context ctx) {
        return ctx.getSharedPreferences(PREFS, Context.MODE_PRIVATE);
    }

    public static boolean getBool(Context ctx, String path, boolean def) {
        try {
            return prefs(ctx).getBoolean(path, def);
        } catch (Exception e) {
            return def;
        }
    }

    public static void setBool(Context ctx, String path, boolean v) {
        prefs(ctx).edit().putBoolean(path, v).apply();
    }

    public static int getInt(Context ctx, String path, int def) {
        try {
            return prefs(ctx).getInt(path, def);
        } catch (Exception e) {
            return def;
        }
    }

    public static void setInt(Context ctx, String path, int v) {
        prefs(ctx).edit().putInt(path, v).apply();
    }

    public static String getString(Context ctx, String path, String def) {
        try {
            String v = prefs(ctx).getString(path, null);
            return v == null ? def : v;
        } catch (Exception e) {
            return def;
        }
    }

    // ---------------- 审计日志（只记摘要，密钥永不明文） ----------------

    private static File auditFile(Context ctx) {
        return new File(ctx.getFilesDir(), "setting_audit.jsonl");
    }

    /** 记录一次配置变更；调用方传入的摘要里绝不能含密钥明文。 */
    public static void logChange(Context ctx, String path, String oldSummary, String newSummary) {
        try {
            if (!prefs(ctx).getBoolean("store.audit_enabled", true)) return;
            String line = "{\"ts\":" + System.currentTimeMillis()
                    + ",\"path\":\"" + esc(path)
                    + "\",\"old\":\"" + esc(trim(oldSummary))
                    + "\",\"new\":\"" + esc(trim(newSummary)) + "\"}\n";
            File f = auditFile(ctx);
            Files.write(f.toPath(), line.getBytes(StandardCharsets.UTF_8),
                    java.nio.file.StandardOpenOption.CREATE,
                    java.nio.file.StandardOpenOption.APPEND);
        } catch (Exception ignored) {
        }
    }

    /** 最近 n 条审计（新→旧），每条已是“path：old → new”的人话行。 */
    public static List<String> readAuditTail(Context ctx, int n) {
        List<String> out = new ArrayList<>();
        try {
            File f = auditFile(ctx);
            if (!f.exists()) return out;
            String txt = new String(Files.readAllBytes(f.toPath()), StandardCharsets.UTF_8);
            String[] lines = txt.split("\n");
            for (int i = lines.length - 1; i >= 0 && out.size() < n; i--) {
                String ln = lines[i].trim();
                if (ln.isEmpty()) continue;
                try {
                    org.json.JSONObject o = new org.json.JSONObject(ln);
                    java.text.SimpleDateFormat fmt = new java.text.SimpleDateFormat(
                            "MM-dd HH:mm", java.util.Locale.getDefault());
                    out.add(fmt.format(new java.util.Date(o.optLong("ts", 0)))
                            + " " + o.optString("path")
                            + "：" + o.optString("old") + " → " + o.optString("new"));
                } catch (Exception ignored) {
                }
            }
        } catch (Exception ignored) {
        }
        return out;
    }

    /** 清除审计日志（清数据类危险操作，调用方必须先弹确认；清除本身不记新行，否则清除无意义）。 */
    public static void clearAudit(Context ctx) {
        try {
            File f = auditFile(ctx);
            if (f.exists()) f.delete();
        } catch (Exception ignored) {
        }
    }

    // ---------------- 危险操作确认门 ----------------

    /** 危险操作写前确认：取消不执行，确认才执行 apply。 */
    public static void confirmDangerous(android.app.Activity act, String title, String msg,
                                        Runnable apply) {
        new android.app.AlertDialog.Builder(act)
                .setTitle(title)
                .setMessage(msg)
                .setNegativeButton("取消", null)
                .setPositiveButton("确认执行", (d, w) -> {
                    try {
                        apply.run();
                    } catch (Exception ignored) {
                    }
                })
                .show();
    }

    /**
     * 停止隧道与节点（危险操作，调用方先弹确认）：
     * 停 Cloudflare 隧道与 tsnet 节点；只停本次运行，
     * 不改开机自启；任一步失败不影响其余步骤。
     */
    public static String restoreDirect(Context ctx) {
        StringBuilder sb = new StringBuilder();
        try {
            CloudflareTunnel.stop(ctx);
            sb.append("隧道已停；");
        } catch (Exception e) {
            sb.append("隧道停失败；");
        }
        try {
            TsnetNode.stop(ctx);
            sb.append("tsnet 节点已停。");
        } catch (Exception e) {
            sb.append("tsnet 停失败。");
        }
        logChange(ctx, "net.restore_direct", "隧道/节点运行中", "已恢复直连");
        return sb.toString();
    }

    private static String trim(String v) {
        if (v == null) return "";
        String t = v.replaceAll("\\s+", " ").trim();
        return t.length() > 120 ? t.substring(0, 120) + "…" : t;
    }

    private static String esc(String v) {
        return v.replace("\\", "\\\\").replace("\"", "\\\"");
    }
}
