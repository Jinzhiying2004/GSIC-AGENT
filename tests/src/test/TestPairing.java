package test;

import com.gsic.app.AgentPrompt;
import com.gsic.app.PairingManager;
import com.gsic.app.TokenManager;

import org.json.JSONObject;

import java.nio.file.Files;
import java.nio.file.Paths;
import java.util.HashSet;
import java.util.List;
import java.util.Set;

/**
 * 配对码 + 设备凭据测试（JVM + stub，不用真机）。
 * 假主 Token：FAKEMASTER0123456789abcdef（仅断言“不含主 Token”用）。
 */
public class TestPairing {

    static final String FAKE_MASTER = "FAKEMASTER0123456789abcdef";
    static int passed;
    static int failed;
    static final long[] NOW = {1_700_000_000_000L};
    static final long DAY = 24 * 3600 * 1000L;
    static final long MIN = 60 * 1000L;

    static void check(boolean cond, String name) {
        if (cond) {
            passed++;
            System.out.println("PASS " + name);
        } else {
            failed++;
            System.out.println("FAIL " + name);
        }
    }

    static void setNow(long t) {
        NOW[0] = t;
    }

    static long base() {
        return 1_700_000_000_000L;
    }

    public static void main(String[] args) throws Exception {
        PairingManager.setClockForTest(() -> NOW[0]);
        try {
            codeFormat();
            codeOneTime();
            codeExpiry();
            codeWrongAndFiveFails();
            codeNewVoidsOld();
            pairFlow();
            deviceExpiry();
            deviceRevoke();
            deviceMaxTen();
            rotateClearsDevices();
            qrAndPromptNoMasterToken();
        } finally {
            PairingManager.clearClockForTest();
        }
        System.out.println("TestPairing: " + passed + " passed, " + failed + " failed");
        if (failed > 0) System.exit(1);
    }

    static void codeFormat() {
        setNow(base());
        Set<String> seen = new HashSet<>();
        boolean ok = true;
        for (int i = 0; i < 50; i++) {
            String c = PairingManager.generateCode();
            if (c.length() != PairingManager.CODE_LEN) ok = false;
            for (int j = 0; j < c.length(); j++) {
                if (PairingManager.CODE_CHARS.indexOf(c.charAt(j)) < 0) ok = false;
            }
            if (c.indexOf('I') >= 0 || c.indexOf('L') >= 0 || c.indexOf('O') >= 0
                    || c.indexOf('0') >= 0 || c.indexOf('1') >= 0) ok = false;
            seen.add(c);
        }
        check(ok, "配对码：8 位、字符集内、无易混淆字符");
        check(seen.size() == 50, "配对码：50 次生成无重复");
    }

    static void codeOneTime() {
        setNow(base());
        String c = PairingManager.generateCode();
        check(PairingManager.validateCode(c), "配对码：首次校验通过");
        check(!PairingManager.validateCode(c), "配对码：一次有效，第二次失败");
    }

    static void codeExpiry() {
        setNow(base());
        String c = PairingManager.generateCode();
        setNow(base() + 11 * MIN);
        check(!PairingManager.validateCode(c), "配对码：10 分钟后过期");
    }

    static void codeWrongAndFiveFails() {
        setNow(base());
        String c = PairingManager.generateCode();
        check(!PairingManager.validateCode("ZZZZZZZZ"), "配对码：错码失败");
        check(PairingManager.validateCode(c), "配对码：1 次失败后正确码仍可用");
        String c2 = PairingManager.generateCode();
        for (int i = 0; i < 5; i++) {
            check(!PairingManager.validateCode("ZZZZZZZZ"), "配对码：第 " + (i + 1) + " 次错码失败");
        }
        check(!PairingManager.validateCode(c2), "配对码：5 次失败后正确码也被作废");
    }

    static void codeNewVoidsOld() {
        setNow(base());
        String a = PairingManager.generateCode();
        String b = PairingManager.generateCode();
        check(!PairingManager.validateCode(a), "配对码：新码生成后旧码失效");
        check(PairingManager.validateCode(b), "配对码：新码可用");
    }

    static void pairFlow() throws Exception {
        setNow(base());
        FakeContext ctx = new FakeContext();
        PairingManager pm = new PairingManager(ctx);
        String code = PairingManager.generateCode();
        JSONObject r = pm.handlePair(
                "{\"code\": \"" + code + "\", \"device\": \"TestAgent\"}");
        check(r.optBoolean("ok", false), "pair：正确配对码返回 ok");
        check(r.optInt("expires_in_days", 0) == 30, "pair：返回 30 天有效期");
        check("TestAgent".equals(r.optString("device", "")), "pair：返回设备名");
        String devToken = r.optString("token", "");
        check(devToken.length() == 32, "pair：签发 32 位设备凭据");
        check(pm.isValidDeviceToken(devToken), "设备凭据：签发后可鉴权");
        check(!pm.isValidDeviceToken(FAKE_MASTER), "设备凭据：假主 Token 不能鉴权");

        String code2 = PairingManager.generateCode();
        JSONObject bad = pm.handlePair("{\"code\": \"ZZZZZZZZ\", \"device\": \"X\"}");
        check(!bad.optBoolean("ok", true)
                && "invalid_code".equals(bad.optString("error", "")),
                "pair：错码返回 invalid_code");
        JSONObject empty = pm.handlePair("{\"code\": \"" + code2 + "\"}");
        check(empty.optBoolean("ok", false)
                && "未命名设备".equals(empty.optString("device", "")),
                "pair：缺 device 时默认未命名设备");
    }

    static void deviceExpiry() {
        setNow(base());
        FakeContext ctx = new FakeContext();
        PairingManager pm = new PairingManager(ctx);
        JSONObject cred = pm.issueDeviceToken("OldDevice");
        String tok = cred.optString("token", "");
        check(pm.isValidDeviceToken(tok), "设备凭据：签发时有效");
        setNow(base() + 31 * DAY);
        check(!pm.isValidDeviceToken(tok), "设备凭据：30 天后过期失效");
    }

    static void deviceRevoke() {
        setNow(base());
        FakeContext ctx = new FakeContext();
        PairingManager pm = new PairingManager(ctx);
        JSONObject d1 = pm.issueDeviceToken("Dev1");
        JSONObject d2 = pm.issueDeviceToken("Dev2");
        String t1 = d1.optString("token", "");
        String t2 = d2.optString("token", "");
        List<JSONObject> list = pm.listDevices();
        check(list.size() == 2, "设备列表：2 个已配对设备");
        boolean noTokenLeak = true;
        for (JSONObject v : list) {
            if (v.has("token")) noTokenLeak = false;
        }
        check(noTokenLeak, "设备列表：不返回 token 明文");
        check(pm.revokeDevice(d1.optString("id", "")), "按设备吊销：返回成功");
        check(!pm.isValidDeviceToken(t1), "按设备吊销：被吊销的失效");
        check(pm.isValidDeviceToken(t2), "按设备吊销：其他的仍有效");
        check(pm.listDevices().size() == 1, "按设备吊销：列表剩 1 个");
    }

    static void deviceMaxTen() {
        setNow(base());
        FakeContext ctx = new FakeContext();
        PairingManager pm = new PairingManager(ctx);
        String first = null;
        for (int i = 0; i < 11; i++) {
            JSONObject d = pm.issueDeviceToken("D" + i);
            if (i == 0) first = d.optString("token", "");
        }
        check(pm.listDevices().size() == 10, "设备凭据：最多保存 10 个");
        check(!pm.isValidDeviceToken(first), "设备凭据：超了去最旧");
    }

    static void rotateClearsDevices() {
        setNow(base());
        FakeContext ctx = new FakeContext();
        PairingManager pm = new PairingManager(ctx);
        JSONObject d = pm.issueDeviceToken("ToClear");
        String tok = d.optString("token", "");
        check(pm.isValidDeviceToken(tok), "轮换前：设备凭据有效");
        new TokenManager(ctx).rotate();
        check(!pm.isValidDeviceToken(tok), "主 Token 轮换后全部设备凭据失效");
        check(pm.listDevices().isEmpty(), "主 Token 轮换后设备列表为空");
    }

    static void qrAndPromptNoMasterToken() throws Exception {
        setNow(base());
        String code = PairingManager.generateCode();
        String qr = AgentPrompt.qrConnectText("100.64.0.1", 28789, code);
        check(qr.contains("100.64.0.1:28789"), "二维码文本：含地址");
        check(qr.contains(code), "二维码文本：含配对码");
        check(qr.contains("/pair"), "二维码文本：含 /pair 指引");
        check(!qr.contains(FAKE_MASTER), "二维码文本：不含主 Token");
        String template = new String(Files.readAllBytes(
                Paths.get("assets/agent_prompt.md")), "UTF-8");
        check(!template.contains("{{TOKEN}}"), "提示词模板：无 {{TOKEN}} 占位符");
        String filled = AgentPrompt.fillTemplate(template, "100.64.0.1", 28789, "TestDevice");
        check(!filled.contains("{{"), "填好后的提示词：无残留占位符");
        check(!filled.contains(FAKE_MASTER), "填好后的提示词：不含主 Token");
        check(filled.contains("/pair"), "填好后的提示词：含配对流程");
        check(filled.contains("100.64.0.1:28789"), "填好后的提示词：地址已填");
    }
}
