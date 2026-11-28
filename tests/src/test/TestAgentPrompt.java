package test;

import android.graphics.Bitmap;

import com.google.zxing.BinaryBitmap;
import com.google.zxing.RGBLuminanceSource;
import com.google.zxing.Result;
import com.google.zxing.common.HybridBinarizer;
import com.google.zxing.qrcode.QRCodeReader;
import com.gsic.app.AgentPrompt;

import java.io.File;
import java.nio.file.Files;

/**
 * 连接提示词/二维码测试（新格式重写，旧版“二维码含完整 Token”的断言已删除）。
 * 假主 Token：FAKEMASTER0123456789abcdef（仅断言“不含主 Token”用）。
 */
public class TestAgentPrompt {

    static final String FAKE_MASTER = "FAKEMASTER0123456789abcdef";
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

    public static void main(String[] args) throws Exception {
        qrText();
        fillTemplateRealAsset();
        loadTemplateFromAssets();
        loadTemplateFallback();
        qrBitmapRoundTrip();
        fillPromptEndToEnd();
        System.out.println("TestAgentPrompt: " + passed + " passed, " + failed + " failed");
        if (failed > 0) System.exit(1);
    }

    static void qrText() {
        String qr = AgentPrompt.qrConnectText("100.64.0.1", 28789, "ABCDEFGH");
        check(qr.contains("100.64.0.1:28789"), "二维码文本：含地址");
        check(qr.contains("ABCDEFGH"), "二维码文本：含配对码");
        check(qr.contains("/pair"), "二维码文本：含 /pair 指引");
        check(!qr.contains(FAKE_MASTER), "二维码文本：不含主 Token");
        check(!qr.contains("Token："), "二维码文本：无 Token 字段");
    }

    static String realTemplate() throws Exception {
        return new String(Files.readAllBytes(new File("assets/agent_prompt.md").toPath()),
                "UTF-8");
    }

    static void fillTemplateRealAsset() throws Exception {
        String filled = AgentPrompt.fillTemplate(realTemplate(), "100.64.0.1", 28789,
                "TestDevice");
        check(!filled.contains("{{"), "填好后的提示词：无残留占位符");
        check(!filled.contains(FAKE_MASTER), "填好后的提示词：不含主 Token");
        check(filled.contains("/pair"), "填好后的提示词：含配对流程");
        check(filled.contains("100.64.0.1:28789"), "填好后的提示词：地址已填");
        check(filled.contains("TestDevice"), "填好后的提示词：设备名已填");
    }

    static void loadTemplateFromAssets() throws Exception {
        FakeContext ctx = new FakeContext();
        ctx.fakeAssets().addFileFromDisk("agent_prompt.md",
                new File("assets/agent_prompt.md"));
        String t = AgentPrompt.loadTemplate(ctx);
        check(t.contains("POST /pair"), "loadTemplate：读到真实模板（含 /pair）");
        check(!t.contains("{{TOKEN}}"), "loadTemplate：真实模板无 {{TOKEN}}");
    }

    static void loadTemplateFallback() {
        FakeContext ctx = new FakeContext();
        String filled = AgentPrompt.fillTemplate(AgentPrompt.loadTemplate(ctx),
                "100.64.0.1", 28789, "TestDevice");
        check(filled.contains("/pair"), "兜底模板：含 /pair 指引");
        check(!filled.contains("{{TOKEN}}"), "兜底模板：无 {{TOKEN}}");
        check(!filled.contains("Token："), "兜底模板：无 Token 字段");
    }

    static void qrBitmapRoundTrip() throws Exception {
        String expected = AgentPrompt.qrConnectText("100.64.0.1", 28789, "ABCDEFGH");
        Bitmap bmp = AgentPrompt.qrBitmap("100.64.0.1", 28789, "ABCDEFGH", 256);
        int w = bmp.getWidth();
        int h = bmp.getHeight();
        int[] px = new int[w * h];
        bmp.getPixels(px, 0, w, 0, 0, w, h);
        RGBLuminanceSource src = new RGBLuminanceSource(w, h, px);
        Result r = new QRCodeReader().decode(new BinaryBitmap(new HybridBinarizer(src)));
        check(expected.equals(r.getText()), "二维码编解码往返：解出文本与 qrConnectText 一致");
    }

    static void fillPromptEndToEnd() throws Exception {
        FakeContext ctx = new FakeContext();
        ctx.fakeAssets().addFileFromDisk("agent_prompt.md",
                new File("assets/agent_prompt.md"));
        String prompt = AgentPrompt.fillPrompt(ctx);
        check(prompt.contains("100.64.0.1:28789"), "fillPrompt：地址来自 stub 节点");
        check(prompt.contains("TestDevice"), "fillPrompt：设备名来自 Build.MODEL");
        check(!prompt.contains("{{TOKEN}}"), "fillPrompt：无 {{TOKEN}}");
        check(!prompt.contains(FAKE_MASTER), "fillPrompt：不含主 Token");
    }
}
