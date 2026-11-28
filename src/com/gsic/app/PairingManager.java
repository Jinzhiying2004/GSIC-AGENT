package com.gsic.app;

import android.content.Context;
import android.util.Log;

import org.json.JSONArray;
import org.json.JSONObject;

import java.nio.charset.StandardCharsets;
import java.security.MessageDigest;
import java.security.SecureRandom;
import java.util.ArrayList;
import java.util.List;
import java.util.UUID;

/**
 * Issue #3：一次性配对码 + 设备专属凭据（主 Token 不再进二维码/提示词/复制内容）。
 *
 * 配对码：8 位（去易混淆字符），SecureRandom 生成；有效期 10 分钟、一次有效、
 * 仅内存保存（进程重启即失效）；同时只存在一个有效码，新生成即作废旧码；
 * 同一码累计 5 次失败即作废；失败一律返回 invalid_code（不区分过期/错误）。
 * 配对码状态是静态的：ApiHandler 与 MainActivity 各自 new 出来的实例共享同一份码，
 * 否则 App 里生成的码到服务端永远对不上。
 *
 * 设备凭据：配对成功签发 UUID 设备 Token，有效期 30 天，经 SecurePrefs 加密存
 * JSON 列表（最多 10 个，清过期、超了去最旧）；支持按设备吊销、列出已配对设备
 * （只给 id/device/剩余天数，绝不返回 token 明文）；主 Token rotate()/revoke()
 * 时清空全部设备凭据（见 TokenManager）。
 */
public class PairingManager {

    private static final String TAG = "PairingManager";
    private static final String KEY_DEVICES = "paired_devices";

    /** 配对码字符集：去 I/L/O/0/1 等易混淆字符 */
    public static final String CODE_CHARS = "ABCDEFGHJKMNPQRSTUVWXYZ23456789";
    public static final int CODE_LEN = 8;
    /** 配对码有效期 10 分钟 */
    public static final long CODE_TTL_MS = 10 * 60 * 1000L;
    /** 同一码累计失败上限，达到即作废 */
    public static final int MAX_FAILS = 5;
    /** 设备凭据有效期 30 天 */
    public static final int DEVICE_EXPIRY_DAYS = 30;
    /** 最多保存的设备凭据数 */
    public static final int MAX_DEVICES = 10;

    private static final SecureRandom RANDOM = new SecureRandom();

    /** 可注入的时钟（仅供 JVM 测试；生产默认系统时间） */
    public interface Clock {
        long now();
    }

    private static volatile Clock testClock;

    /** 仅供测试：注入固定时钟；传 null 恢复系统时间 */
    public static void setClockForTest(Clock c) {
        testClock = c;
    }

    public static void clearClockForTest() {
        testClock = null;
    }

    static long now() {
        Clock c = testClock;
        return c != null ? c.now() : System.currentTimeMillis();
    }

    // ---- 配对码（静态内存态，全进程共享） ----

    private static String currentCode;
    private static long codeCreatedMs;
    private static int failCount;

    /** 生成新配对码（旧码立即作废，失败计数清零） */
    public static synchronized String generateCode() {
        StringBuilder sb = new StringBuilder(CODE_LEN);
        for (int i = 0; i < CODE_LEN; i++) {
            sb.append(CODE_CHARS.charAt(RANDOM.nextInt(CODE_CHARS.length())));
        }
        currentCode = sb.toString();
        codeCreatedMs = now();
        failCount = 0;
        return currentCode;
    }

    /** 取当前有效配对码；无码或已过期则生成新的 */
    public static synchronized String getOrCreateCode() {
        if (currentCode != null && now() - codeCreatedMs <= CODE_TTL_MS) {
            return currentCode;
        }
        return generateCode();
    }

    /** 当前配对码剩余有效期（毫秒）；无码返回 -1，已过期返回 0 */
    public static synchronized long getCodeRemainingMs() {
        if (currentCode == null) return -1;
        return Math.max(0, codeCreatedMs + CODE_TTL_MS - now());
    }

    /** 校验配对码：成功即消费（一次有效）；失败计次，满 5 次作废 */
    public static synchronized boolean validateCode(String code) {
        if (code == null || currentCode == null) return false;
        if (now() - codeCreatedMs > CODE_TTL_MS) {
            invalidateCode();
            return false;
        }
        if (constantTimeEquals(code, currentCode)) {
            invalidateCode();
            return true;
        }
        failCount++;
        if (failCount >= MAX_FAILS) invalidateCode();
        return false;
    }

    private static void invalidateCode() {
        currentCode = null;
        codeCreatedMs = 0;
        failCount = 0;
    }

    private static boolean constantTimeEquals(String a, String b) {
        return MessageDigest.isEqual(
                a.getBytes(StandardCharsets.UTF_8),
                b.getBytes(StandardCharsets.UTF_8));
    }

    // ---- 设备凭据（SecurePrefs 加密存 JSON 列表） ----

    private final Context ctx;
    private final SecurePrefs sp;

    public PairingManager(Context ctx) {
        this.ctx = ctx.getApplicationContext();
        this.sp = new SecurePrefs(this.ctx, "agent");
    }

    /** 供 TokenManager.rotate()/revoke() 调用：清空全部设备凭据 */
    public static void clearAllDevices(Context ctx) {
        try {
            new PairingManager(ctx).clearAll();
        } catch (Exception ignored) {
        }
    }

    public synchronized void clearAll() {
        sp.remove(KEY_DEVICES);
    }

    /** 读设备列表（内存里清掉过期项；过期项清掉后会尽力回写） */
    synchronized JSONArray loadDevices() {
        JSONArray arr = new JSONArray();
        String raw = sp.getSecure(KEY_DEVICES, null);
        if (raw != null && !raw.isEmpty()) {
            try {
                arr = new JSONArray(raw);
            } catch (Exception e) {
                Log.w(TAG, "已配对设备列表解析失败，视为空");
                arr = new JSONArray();
            }
        }
        int before = arr.length();
        purgeExpired(arr);
        if (arr.length() != before) saveDevices(arr);
        return arr;
    }

    private void purgeExpired(JSONArray arr) {
        long t = now();
        for (int i = arr.length() - 1; i >= 0; i--) {
            try {
                JSONObject d = arr.getJSONObject(i);
                long created = d.optLong("createdMs", 0);
                int days = d.optInt("expiryDays", DEVICE_EXPIRY_DAYS);
                if (created <= 0 || t - created > (long) days * 24 * 3600 * 1000) {
                    arr.remove(i);
                }
            } catch (Exception ignored) {
            }
        }
    }

    /** 保存设备列表；Keystore 加密失败返回 false（调用方拒绝签发） */
    private boolean saveDevices(JSONArray arr) {
        return sp.putSecure(KEY_DEVICES, arr.toString());
    }

    /** 签发设备凭据；加密保存失败返回 null（拒绝发一个存不下的凭据） */
    public synchronized JSONObject issueDeviceToken(String device) {
        try {
            JSONArray arr = loadDevices();
            while (arr.length() >= MAX_DEVICES) arr.remove(0);
            JSONObject d = new JSONObject();
            d.put("id", UUID.randomUUID().toString().replace("-", ""));
            d.put("token", UUID.randomUUID().toString().replace("-", ""));
            d.put("device", device == null ? "" : device);
            d.put("createdMs", now());
            d.put("expiryDays", DEVICE_EXPIRY_DAYS);
            arr.put(d);
            if (!saveDevices(arr)) return null;
            return d;
        } catch (Exception e) {
            Log.e(TAG, "签发设备凭据失败", e);
            return null;
        }
    }

    /** 校验设备 Token（含过期检查） */
    public synchronized boolean isValidDeviceToken(String token) {
        if (token == null || token.isEmpty()) return false;
        try {
            JSONArray arr = loadDevices();
            for (int i = 0; i < arr.length(); i++) {
                JSONObject d = arr.getJSONObject(i);
                if (constantTimeEquals(token, d.optString("token", ""))) {
                    long created = d.optLong("createdMs", 0);
                    int days = d.optInt("expiryDays", DEVICE_EXPIRY_DAYS);
                    return created > 0
                            && now() - created <= (long) days * 24 * 3600 * 1000;
                }
            }
        } catch (Exception ignored) {
        }
        return false;
    }

    /** 列出已配对设备：只给 id/device/剩余天数，绝不返回 token 明文 */
    public synchronized List<JSONObject> listDevices() {
        List<JSONObject> out = new ArrayList<>();
        try {
            JSONArray arr = loadDevices();
            for (int i = 0; i < arr.length(); i++) {
                JSONObject d = arr.getJSONObject(i);
                long created = d.optLong("createdMs", 0);
                int days = d.optInt("expiryDays", DEVICE_EXPIRY_DAYS);
                long remainMs = created + (long) days * 24 * 3600 * 1000 - now();
                int remainDays = remainMs <= 0 ? 0 : (int) (remainMs / 86400000L);
                JSONObject v = new JSONObject();
                v.put("id", d.optString("id", ""));
                v.put("device", d.optString("device", ""));
                v.put("remainingDays", remainDays);
                out.add(v);
            }
        } catch (Exception ignored) {
        }
        return out;
    }

    /** 按设备吊销：删掉指定 id 的凭据 */
    public synchronized boolean revokeDevice(String id) {
        if (id == null || id.isEmpty()) return false;
        try {
            JSONArray arr = loadDevices();
            for (int i = 0; i < arr.length(); i++) {
                if (id.equals(arr.getJSONObject(i).optString("id", ""))) {
                    arr.remove(i);
                    return saveDevices(arr);
                }
            }
        } catch (Exception ignored) {
        }
        return false;
    }

    /**
     * 处理 POST /pair：body {"code","device"}。
     * 成功返回 {"ok":true,"token":设备凭据,"expires_in_days":30,"device":...}；
     * 配对码错误/过期/被作废一律返回 invalid_code（不区分原因，防枚举）；
     * 凭据存不下（Keystore 异常）返回 pairing_failed。
     */
    public JSONObject handlePair(String body) {
        JSONObject r = new JSONObject();
        try {
            JSONObject bj = new JSONObject(body == null ? "" : body);
            String code = bj.optString("code", "").trim();
            String device = bj.optString("device", "").trim();
            if (device.isEmpty()) device = "未命名设备";
            if (device.length() > 64) device = device.substring(0, 64);
            if (code.isEmpty() || !validateCode(code)) {
                return r.put("ok", false).put("error", "invalid_code");
            }
            JSONObject cred = issueDeviceToken(device);
            if (cred == null) {
                return r.put("ok", false).put("error", "pairing_failed");
            }
            return r.put("ok", true)
                    .put("token", cred.getString("token"))
                    .put("expires_in_days", DEVICE_EXPIRY_DAYS)
                    .put("device", device);
        } catch (Exception e) {
            try {
                return r.put("ok", false).put("error", "invalid_code");
            } catch (Exception ignored) {
                return new JSONObject();
            }
        }
    }
}
