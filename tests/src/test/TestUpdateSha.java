package test;

import com.gsic.app.UpdateManager;

import java.io.File;
import java.nio.file.Files;
import java.security.MessageDigest;

/** 自更新 SHA-256 测试（JVM + stub，不用真机）。 */
public class TestUpdateSha {

    static int passed;
    static int failed;

    static void check(boolean cond, String name) {
        if (cond) {
            passed++;
            System.out.println("PASS " + name);
        } else {
            failed++;
            System.out.println("FAIL " + name);
        }
    }

    static String sha256Hex(byte[] data) throws Exception {
        byte[] d = MessageDigest.getInstance("SHA-256").digest(data);
        StringBuilder sb = new StringBuilder(64);
        for (byte b : d) sb.append(String.format("%02x", b & 0xff));
        return sb.toString();
    }

    public static void main(String[] args) throws Exception {
        File dir = Files.createTempDirectory("updsha").toFile();
        byte[] data = "gsic fake apk content".getBytes("UTF-8");
        File f = new File(dir, "a.apk");
        Files.write(f.toPath(), data);
        String good = sha256Hex(data);

        check(UpdateManager.verifySha256(f, good), "verifySha256：匹配通过");
        check(UpdateManager.verifySha256(f, good.toUpperCase()), "verifySha256：大写期望值也通过");
        String bad = good.substring(0, 63) + (good.charAt(63) == '0' ? '1' : '0');
        check(!UpdateManager.verifySha256(f, bad), "verifySha256：不匹配拒绝");
        check(!UpdateManager.verifySha256(f, null), "verifySha256：期望值 null 拒绝");
        check(!UpdateManager.verifySha256(f, "short"), "verifySha256：期望值格式错拒绝");
        check(!UpdateManager.verifySha256(new File(dir, "nope.apk"), good),
                "verifySha256：文件缺失拒绝");
        File empty = new File(dir, "empty.apk");
        Files.write(empty.toPath(), new byte[0]);
        check(UpdateManager.verifySha256(empty, sha256Hex(new byte[0])),
                "verifySha256：空文件按空 hash 校验");
        check(!UpdateManager.verifySha256(empty, good), "verifySha256：空文件对不上非空 hash");

        check(!UpdateManager.isValidSha256(null), "isValidSha256：null 拒绝");
        check(!UpdateManager.isValidSha256(""), "isValidSha256：空串拒绝");
        check(!UpdateManager.isValidSha256(good.substring(0, 63)), "isValidSha256：63 位拒绝");
        check(!UpdateManager.isValidSha256(good + "a"), "isValidSha256：65 位拒绝");
        check(!UpdateManager.isValidSha256(
                "gggggggggggggggggggggggggggggggggggggggggggggggggggggggggggggggg"),
                "isValidSha256：非 hex 拒绝");
        check(!UpdateManager.isValidSha256(good + " "), "isValidSha256：带空格拒绝");
        check(UpdateManager.isValidSha256(good), "isValidSha256：小写 64 位通过");
        check(UpdateManager.isValidSha256(good.toUpperCase()), "isValidSha256：大写 64 位通过");
        check(UpdateManager.isValidSha256(
                "aAbBcCdDeEfF00112233445566778899aAbBcCdDeEfF00112233445566778899"),
                "isValidSha256：大小写混合通过");

        System.out.println("TestUpdateSha: " + passed + " passed, " + failed + " failed");
        if (failed > 0) System.exit(1);
    }
}
