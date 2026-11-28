package com.gsic.app;

import java.nio.charset.StandardCharsets;

/**
 * JVM 测试桩：可逆占位加密（与生产 CryptoUtil 同 API：ENC: 前缀 + Base64）。
 * 生产用 Keystore AES/GCM（见 src 同名类），测试只验证“加密落盘/解密读回”的
 * 往返形状，不验证密码学强度。
 */
public class CryptoUtil {

    private static final String PREFIX = "ENC:";

    public static boolean isEncrypted(String v) {
        return v != null && v.startsWith(PREFIX);
    }

    public static String encrypt(String plain) {
        if (plain == null || plain.isEmpty()) return plain == null ? "" : plain;
        return PREFIX + java.util.Base64.getEncoder()
                .encodeToString(plain.getBytes(StandardCharsets.UTF_8));
    }

    public static String decrypt(String v) {
        if (v == null || v.isEmpty()) return v == null ? "" : v;
        if (!isEncrypted(v)) return v;
        try {
            return new String(java.util.Base64.getDecoder()
                    .decode(v.substring(PREFIX.length())), StandardCharsets.UTF_8);
        } catch (Exception e) {
            return "";
        }
    }
}
