package com.gsic.app;

import android.content.Context;
import android.graphics.Bitmap;
import android.os.Build;

import com.google.zxing.BarcodeFormat;
import com.google.zxing.EncodeHintType;
import com.google.zxing.WriterException;
import com.google.zxing.common.BitMatrix;
import com.google.zxing.qrcode.QRCodeWriter;

import java.io.ByteArrayOutputStream;
import java.io.InputStream;
import java.util.HashMap;
import java.util.Map;

/**
 * v4.33：连接智能体——与电脑版同款的 /agent-prompt 接口 + 二维码。
 * 二维码只放地址 + Token（完整提示词约 4KB，装不进二维码），
 * 智能体扫码连上后调 GET /agent-prompt 取全文。
 * 模板在 assets/agent_prompt.md；读不到时用最小内置模板兜底。
 */
public class AgentPrompt {

    private static final String TEMPLATE_ASSET = "agent_prompt.md";

    /** 平台说明（Android），填模板的 {{PLATFORM_NOTES}} 段。 */
    private static final String PLATFORM_NOTES =
            "## 平台说明（Android）\n\n"
            + "- 坐标是屏幕物理像素，左上角是 (0, 0)。\n"
            + "- shell 步骤是 root shell；读文件时文件若无尾换行，用 `base64 <文件>` 再解码，"
            + "或命令尾加 `; echo`，否则命令可能挂起。\n"
            + "- 步骤任务默认在虚拟屏跑（软件自动建屏，实体屏右上角小窗预览）；"
            + "一个任务钉死一个屏，中途不跨屏。\n"
            + "- Termux 已装，可用 termux 步骤跑 python/git 等命令。\n\n"
            + "## 例子：打开设置看一眼\n\n"
            + "1. GET /status 确认在线\n"
            + "2. POST /task：{\"steps\": [{\"do\": \"launch\", \"package\": \"com.android.settings\"}, "
            + "{\"do\": \"sleep\", \"s\": 1}, {\"do\": \"screenshot\"}]}\n"
            + "3. 看截图确认设置打开了";

    /** 最小内置模板兜底（assets 模板丢失时用，保证 /agent-prompt 仍可用）。 */
    private static final String FALLBACK_TEMPLATE =
            "# GSIC 远控连接信息\n\n"
            + "- 远控地址：http://{{TAILNET_IP}}:{{PORT}}\n"
            + "- Token：{{TOKEN}}\n"
            + "- 设备：{{HOSTNAME}}（{{PLATFORM}}）\n"
            + "- 每个请求都要带请求头：`X-Token: {{TOKEN}}`\n\n"
            + "接口：GET /status、GET /screen、GET /screenshot、POST /action（单个步骤）、"
            + "POST /task（{\"steps\": [...]}）、GET /task/status、POST /task/stop。\n\n"
            + "{{PLATFORM_NOTES}}\n\n"
            + "规矩：先截图或查 /screen 再动手，绝不猜坐标；一次只发一个请求；"
            + "超时 60 秒以上；做完截图验证；Token 不外传。";

    /** 二维码内容：与电脑版同格式，只放地址 + Token + 取全文指引。 */
    public static String qrConnectText(String ip, int port, String token) {
        return "GSIC 远控连接\n地址：http://" + ip + ":" + port + "\nToken：" + token
                + "\n连上后先用 X-Token 请求头调 GET /agent-prompt 取完整操作说明，再开始工作。";
    }

    /** 读模板（assets/agent_prompt.md），读不到时用最小内置模板兜底。 */
    public static String loadTemplate(Context ctx) {
        try {
            InputStream in = ctx.getAssets().open(TEMPLATE_ASSET);
            ByteArrayOutputStream out = new ByteArrayOutputStream();
            byte[] buf = new byte[4096];
            int n;
            while ((n = in.read(buf)) > 0) out.write(buf, 0, n);
            in.close();
            String t = new String(out.toByteArray(), "UTF-8");
            if (t.contains("{{TOKEN}}")) return t;
        } catch (Exception ignored) {
        }
        return FALLBACK_TEMPLATE;
    }

    /** 纯字符串填充（无 Android 依赖，可 JVM 单元测）。 */
    public static String fillTemplate(String template, String ip, int port,
                                      String token, String hostname) {
        return template
                .replace("{{TAILNET_IP}}", ip == null ? "" : ip)
                .replace("{{PORT}}", String.valueOf(port))
                .replace("{{TOKEN}}", token == null ? "" : token)
                .replace("{{HOSTNAME}}", hostname == null ? "-" : hostname)
                .replace("{{PLATFORM}}", "Android")
                .replace("{{PLATFORM_NOTES}}", PLATFORM_NOTES);
    }

    /** 取本机实时连接信息填好全文（供 /agent-prompt 与 App 内复制共用）。 */
    public static String fillPrompt(Context ctx) {
        String ip = TsnetNode.getTailIp();
        int port = AgentService.getPort(ctx);
        String token = new TokenManager(ctx).getToken();
        String host = Build.MODEL != null ? Build.MODEL : "-";
        return fillTemplate(loadTemplate(ctx), ip, port, token, host);
    }

    /** 生成连接二维码 Bitmap（sizePx 边长像素，ZXing 默认留白边保证可扫）。 */
    public static Bitmap qrBitmap(String ip, int port, String token, int sizePx)
            throws WriterException {
        Map<EncodeHintType, Object> hints = new HashMap<>();
        hints.put(EncodeHintType.CHARACTER_SET, "UTF-8");
        BitMatrix m = new QRCodeWriter().encode(
                qrConnectText(ip, port, token),
                BarcodeFormat.QR_CODE, sizePx, sizePx, hints);
        int w = m.getWidth(), h = m.getHeight();
        int[] px = new int[w * h];
        for (int y = 0; y < h; y++) {
            for (int x = 0; x < w; x++) {
                px[y * w + x] = m.get(x, y) ? 0xFF000000 : 0xFFFFFFFF;
            }
        }
        Bitmap bmp = Bitmap.createBitmap(w, h, Bitmap.Config.ARGB_8888);
        bmp.setPixels(px, 0, w, 0, 0, w, h);
        return bmp;
    }
}
