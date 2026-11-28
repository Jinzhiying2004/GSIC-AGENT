package com.gsic.app;

import android.content.Context;
import android.content.Intent;
import android.net.Uri;
import android.os.Build;
import android.provider.Settings;

import org.json.JSONObject;

import java.io.BufferedInputStream;
import java.io.ByteArrayOutputStream;
import java.io.File;
import java.io.FileOutputStream;
import java.io.InputStream;
import java.io.OutputStream;
import java.net.HttpURLConnection;
import java.net.URL;
import java.security.MessageDigest;

/**
 * 应用内更新（热更新的应用内安装形态，非热补丁）。
 * 更新源为一个可配置的 version.json 地址，格式：
 * {"version":"2.43","versionCode":68,"apkUrl":"https://.../gsic-2.43.apk","notes":"更新说明"}
 * 所有回调都在后台线程触发，UI 侧自行 runOnUiThread。
 */
public class UpdateManager {

    public interface CheckListener {
        void onResult(boolean hasUpdate, String version, int versionCode,
                      String apkUrl, String notes, String err, String sha256);
    }

    public interface DownloadListener {
        void onProgress(int percent);

        void onDone(File apk, String err);
    }

    /**
     * 内置默认更新源（地基·自更新写死）：GitHub 仓库根目录的 version.json，
     * apkUrl 指向对应 Release 附件。用户在设置里填了源则以用户的为准。
     */
    public static final String DEFAULT_SOURCE =
            "https://raw.githubusercontent.com/Jinzhiying2004/GSIC-AGENT/main/version.json";

    public static String getSource(Context ctx) {
        String s = ctx.getSharedPreferences("update", Context.MODE_PRIVATE)
                .getString("source", "");
        return (s == null || s.trim().isEmpty()) ? DEFAULT_SOURCE : s;
    }

    public static void setSource(Context ctx, String url) {
        ctx.getSharedPreferences("update", Context.MODE_PRIVATE)
                .edit().putString("source", url == null ? "" : url.trim()).apply();
    }

    public static int currentCode(Context ctx) {
        try {
            return ctx.getPackageManager()
                    .getPackageInfo(ctx.getPackageName(), 0).versionCode;
        } catch (Exception e) {
            return 0;
        }
    }

    /** 检查更新源的 version.json，与本地 versionCode 比较 */
    public static void check(Context ctx, CheckListener cb) {
        final Context app = ctx.getApplicationContext();
        final String src = getSource(app);
        if (src == null || src.trim().isEmpty()) {
            cb.onResult(false, null, 0, null, null,
                    "未设置更新源，请先设置更新源地址", null);
            return;
        }
        new Thread(() -> {
            HttpURLConnection c = null;
            try {
                c = (HttpURLConnection) new URL(src.trim()).openConnection();
                c.setConnectTimeout(15000);
                c.setReadTimeout(20000);
                c.setRequestProperty("User-Agent", "gsic");
                c.connect();
                if (c.getResponseCode() != 200) {
                    cb.onResult(false, null, 0, null, null,
                            "更新源返回 " + c.getResponseCode(), null);
                    return;
                }
                String body = readAll(c.getInputStream());
                JSONObject j = new JSONObject(body);
                int rc = j.optInt("versionCode", 0);
                String rv = j.optString("version", "");
                if (rc > currentCode(app)) {
                    // Issue #3（与 Windows #4 同款硬拒绝口径）：有新版时
                    // version.json 必须带有效 SHA-256，否则拒绝更新
                    String sha = j.optString("sha256", "");
                    if (!isValidSha256(sha)) {
                        cb.onResult(false, rv, rc, null, null,
                                "更新源缺少 SHA-256，拒绝更新", null);
                        return;
                    }
                    cb.onResult(true, rv, rc, j.optString("apkUrl", ""),
                            j.optString("notes", ""), null, sha);
                } else {
                    cb.onResult(false, rv, rc, null, null, null, null);
                }
            } catch (Exception e) {
                cb.onResult(false, null, 0, null, null,
                        "检查失败：" + e.getMessage(), null);
            } finally {
                if (c != null) c.disconnect();
            }
        }).start();
    }

    /** 下载 APK 到私有目录 files/updates/update.apk，带进度；
     * 下完先验 SHA-256，不匹配删文件并报错（Issue #3，硬拒绝口径） */
    public static void download(Context ctx, String apkUrl, String expectedSha256,
                                DownloadListener dl) {
        final Context app = ctx.getApplicationContext();
        new Thread(() -> {
            HttpURLConnection c = null;
            try {
                c = (HttpURLConnection) new URL(apkUrl).openConnection();
                c.setConnectTimeout(15000);
                c.setReadTimeout(60000);
                c.setRequestProperty("User-Agent", "gsic");
                c.connect();
                if (c.getResponseCode() != 200) {
                    dl.onDone(null, "下载失败，更新源返回 " + c.getResponseCode());
                    return;
                }
                int total = c.getContentLength();
                File dir = new File(app.getFilesDir(), "updates");
                dir.mkdirs();
                File out = new File(dir, "update.apk");
                try (InputStream in = new BufferedInputStream(c.getInputStream());
                     OutputStream os = new FileOutputStream(out)) {
                    byte[] buf = new byte[65536];
                    int n;
                    long done = 0;
                    int last = -1;
                    while ((n = in.read(buf)) > 0) {
                        os.write(buf, 0, n);
                        done += n;
                        if (total > 0) {
                            int p = (int) (done * 100 / total);
                            if (p != last) {
                                last = p;
                                dl.onProgress(p);
                            }
                        }
                    }
                }
                if (!verifySha256(out, expectedSha256)) {
                    out.delete();
                    dl.onDone(null, "SHA-256 校验失败，安装包可能被篡改，已丢弃");
                    return;
                }
                dl.onDone(out, null);
            } catch (Exception e) {
                dl.onDone(null, "下载失败：" + e.getMessage());
            } finally {
                if (c != null) c.disconnect();
            }
        }).start();
    }

    /** SHA-256 是否为 64 位十六进制（大小写均可） */
    public static boolean isValidSha256(String s) {
        if (s == null || s.length() != 64) return false;
        for (int i = 0; i < s.length(); i++) {
            char c = s.charAt(i);
            if (!((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')
                    || (c >= 'A' && c <= 'F'))) return false;
        }
        return true;
    }

    /** 校验文件 SHA-256（小写比较）；文件缺失/读失败/期望值非法都返回 false */
    public static boolean verifySha256(File f, String expected) {
        if (f == null || !f.isFile() || !isValidSha256(expected)) return false;
        try {
            MessageDigest md = MessageDigest.getInstance("SHA-256");
            try (InputStream in = new java.io.FileInputStream(f)) {
                byte[] buf = new byte[65536];
                int n;
                while ((n = in.read(buf)) > 0) md.update(buf, 0, n);
            }
            byte[] digest = md.digest();
            StringBuilder sb = new StringBuilder(64);
            for (byte b : digest) sb.append(String.format("%02x", b & 0xff));
            return sb.toString().equals(expected.toLowerCase(java.util.Locale.US));
        } catch (Exception e) {
            return false;
        }
    }

    /**
     * 安装下载好的 APK。地基·自更新写死：有 root 时优先静默安装
     * （pm install -r，不弹系统安装器），装前落待确认戳，服务重启后
     * 由 confirmIfUpdated 对账成「更新成功回执」；无 root/静默失败
     * 再回退系统安装器。
     * 返回 "silent_ok" 表示已静默安装；null 表示已调起系统安装；
     * "need_permission" 表示需允许“安装未知应用”；其他为错误原因。
     */
    public static String installApk(Context ctx, File apk) {
        // 待确认戳：先从待装 APK 读出目标 versionCode
        int targetCode = 0;
        try {
            android.content.pm.PackageInfo pi = ctx.getPackageManager()
                    .getPackageArchiveInfo(apk.getAbsolutePath(), 0);
            if (pi != null) targetCode = pi.versionCode;
        } catch (Exception ignored) {
        }
        // root 静默安装
        try {
            if (Root.findSu() != null) {
                if (targetCode > 0) {
                    ctx.getSharedPreferences("update", Context.MODE_PRIVATE)
                            .edit().putInt("pending_code", targetCode)
                            .putLong("pending_ts", System.currentTimeMillis())
                            .apply();
                }
                Process p = Root.exec("pm install -r "
                        + apk.getAbsolutePath());
                int rc = p.waitFor();
                if (rc == 0) return "silent_ok";
                // 静默失败：清待确认戳，回退系统安装
                ctx.getSharedPreferences("update", Context.MODE_PRIVATE)
                        .edit().remove("pending_code").apply();
            }
        } catch (Exception ignored) {
        }
        if (Build.VERSION.SDK_INT >= 26
                && !ctx.getPackageManager().canRequestPackageInstalls()) {
            return "need_permission";
        }
        try {
            Uri uri = UpdateProvider.uriFor(apk);
            Intent it = new Intent(Intent.ACTION_VIEW);
            it.setDataAndType(uri, "application/vnd.android.package-archive");
            it.addFlags(Intent.FLAG_GRANT_READ_URI_PERMISSION
                    | Intent.FLAG_ACTIVITY_NEW_TASK);
            ctx.startActivity(it);
            return null;
        } catch (Exception e) {
            return e.getMessage();
        }
    }

    /**
     * 装后自验（AgentService 每次启动时调）：待确认戳的目标版本与当前
     * 版本对上（或已被更新的版本越过），即落「更新成功回执」并清戳——
     * 与开机回执同一思路：地基能力要有证据，不靠"应该装上了"。
     */
    public static void confirmIfUpdated(Context ctx) {
        try {
            android.content.SharedPreferences p =
                    ctx.getSharedPreferences("update", Context.MODE_PRIVATE);
            int pending = p.getInt("pending_code", 0);
            if (pending <= 0) return;
            int cur = currentCode(ctx);
            if (cur >= pending) {
                p.edit().putLong("update_ok_ts", System.currentTimeMillis())
                        .putInt("update_ok_code", cur)
                        .remove("pending_code").apply();
            }
        } catch (Exception ignored) {
        }
    }

    /** 自更新回执文本（自检卡展示） */
    public static String receiptText(Context ctx) {
        try {
            android.content.SharedPreferences p =
                    ctx.getSharedPreferences("update", Context.MODE_PRIVATE);
            java.text.SimpleDateFormat f = new java.text.SimpleDateFormat(
                    "MM-dd HH:mm", java.util.Locale.getDefault());
            long okTs = p.getLong("update_ok_ts", 0);
            if (okTs > 0) {
                return "🔄 自更新：上次成功（code "
                        + p.getInt("update_ok_code", 0) + "，"
                        + f.format(new java.util.Date(okTs)) + "）";
            }
            int pending = p.getInt("pending_code", 0);
            if (pending > 0) return "🔄 自更新：目标 code " + pending + " 待确认";
            String avail = p.getString("avail_version", "");
            if (!avail.isEmpty()) return "🔄 自更新：有新版 " + avail + " 可装";
            return "🔄 自更新：暂无记录";
        } catch (Exception e) {
            return "🔄 自更新：读取失败";
        }
    }

    /** 服务启动时静默检查（24 小时节流）：只记录有无新版，不打扰用户 */
    public static void silentCheck(Context ctx) {
        try {
            android.content.SharedPreferences p =
                    ctx.getSharedPreferences("update", Context.MODE_PRIVATE);
            long last = p.getLong("last_check_ts", 0);
            if (System.currentTimeMillis() - last < 24 * 3600_000L) return;
            p.edit().putLong("last_check_ts", System.currentTimeMillis()).apply();
            check(ctx, (hasUpdate, version, code, apkUrl, notes, err, sha256) -> {
                try {
                    android.content.SharedPreferences.Editor e =
                            ctx.getSharedPreferences("update",
                                    Context.MODE_PRIVATE).edit();
                    if (err == null && hasUpdate) {
                        e.putString("avail_version", version)
                                .putInt("avail_code", code)
                                .putString("avail_sha256",
                                        sha256 == null ? "" : sha256);
                    } else if (err == null) {
                        e.remove("avail_version").remove("avail_code")
                                .remove("avail_sha256");
                    }
                    e.apply();
                } catch (Exception ignored) {
                }
            });
        } catch (Exception ignored) {
        }
    }

    /** 跳转到“允许安装未知应用”设置页（本应用） */
    public static void openUnknownSourcesSetting(Context ctx) {
        try {
            Intent it = new Intent(Settings.ACTION_MANAGE_UNKNOWN_APP_SOURCES,
                    Uri.parse("package:" + ctx.getPackageName()));
            it.addFlags(Intent.FLAG_ACTIVITY_NEW_TASK);
            ctx.startActivity(it);
        } catch (Exception e) {
            ctx.startActivity(new Intent(Settings.ACTION_SECURITY_SETTINGS)
                    .addFlags(Intent.FLAG_ACTIVITY_NEW_TASK));
        }
    }

    private static String readAll(InputStream in) throws Exception {
        ByteArrayOutputStream bos = new ByteArrayOutputStream();
        byte[] buf = new byte[8192];
        int n;
        while ((n = in.read(buf)) > 0) bos.write(buf, 0, n);
        return bos.toString("UTF-8");
    }
}
