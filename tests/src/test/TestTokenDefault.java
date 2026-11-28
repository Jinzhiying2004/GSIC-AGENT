package test;

import com.gsic.app.TokenManager;

/** Token 默认有效期 3 年测试（JVM + stub，不用真机）。 */
public class TestTokenDefault {

    static int passed;
    static int failed;
    static final long DAY = 24 * 3600 * 1000L;

    static void check(boolean cond, String name) {
        if (cond) {
            passed++;
            System.out.println("PASS " + name);
        } else {
            failed++;
            System.out.println("FAIL " + name);
        }
    }

    public static void main(String[] args) {
        freshDefaultIs3Years();
        fiftyDayOldTokenNoRotate();
        explicitNeverStays();
        newTokenWrites3Years();
        expiredTokenRotates();
        System.out.println("TestTokenDefault: " + passed + " passed, " + failed + " failed");
        if (failed > 0) System.exit(1);
    }

    /** 全新偏好下默认 1095 天。 */
    static void freshDefaultIs3Years() {
        FakeContext ctx = new FakeContext();
        TokenManager tm = new TokenManager(ctx);
        check(tm.getExpiryDays() == 1095, "全新偏好：getExpiryDays()==1095");
    }

    /** 创建 50 天前的 Token，在新默认（1095 天）下不轮换。 */
    static void fiftyDayOldTokenNoRotate() {
        FakeContext ctx = new FakeContext();
        TokenManager tm = new TokenManager(ctx);
        String t1 = tm.getToken();
        // 模拟旧安装：Token 50 天前创建，且从未存过有效期偏好
        ctx.prefFile("agent").edit()
                .putLong("token_time", System.currentTimeMillis() - 50 * DAY)
                .remove("token_expiry_days")
                .apply();
        String t2 = tm.getToken();
        check(t1.equals(t2), "50 天前的 Token 在新默认下不轮换");
        check(tm.getExpiryDays() == 1095, "50 天前的 Token：默认读到 1095");
    }

    /** 用户显式选过永久（0）的不被改写。 */
    static void explicitNeverStays() {
        FakeContext ctx = new FakeContext();
        TokenManager tm = new TokenManager(ctx);
        tm.setExpiryDays(0);
        ctx.prefFile("agent").edit().remove("token").apply();
        tm.getToken();
        check(tm.getExpiryDays() == 0, "显式选永久：重新生成后仍是 0");
    }

    /** 新 Token 生成且未存过偏好时，写入 1095。 */
    static void newTokenWrites3Years() {
        FakeContext ctx = new FakeContext();
        TokenManager tm = new TokenManager(ctx);
        check(!ctx.prefFile("agent").contains("token_expiry_days"), "新生成前：无偏好");
        tm.getToken();
        check(ctx.prefFile("agent").getInt("token_expiry_days", -1) == 1095,
                "新 Token 生成时写入 1095 偏好");
    }

    /** 超过 1095 天的 Token 会轮换（边界对照）。 */
    static void expiredTokenRotates() {
        FakeContext ctx = new FakeContext();
        TokenManager tm = new TokenManager(ctx);
        String t1 = tm.getToken();
        ctx.prefFile("agent").edit()
                .putLong("token_time", System.currentTimeMillis() - 1096 * DAY)
                .remove("token_expiry_days")
                .apply();
        String t2 = tm.getToken();
        check(!t1.equals(t2), "1096 天前的 Token 按 3 年默认轮换");
    }
}
