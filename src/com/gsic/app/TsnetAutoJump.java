package com.gsic.app;

/**
 * 网页登录自动跳转 guard（纯 Java，不依赖 Android 类，便于 JVM 单测）。
 * 语义：同一链接只自动跳一次；链接被清空/过期（调用方观测到空链接时调
 * shouldJump("") 或 reset()）后 guard 复位，新链接到达可再次跳。
 * 注意：guard 只在“决定跳转”时标记；若浏览器拉起抛异常，调用方必须
 * 调 reset()，否则下一次通知到达时无法重试（手动按钮仍可用）。
 */
public class TsnetAutoJump {

    private String lastJumpedUrl = "";

    /** 空链接时复位并返回 false；新链接返回 true 并标记；同一链接返回 false。 */
    public synchronized boolean shouldJump(String url) {
        if (url == null || url.isEmpty()) {
            lastJumpedUrl = "";
            return false;
        }
        if (url.equals(lastJumpedUrl)) {
            return false;
        }
        lastJumpedUrl = url;
        return true;
    }

    public synchronized void reset() {
        lastJumpedUrl = "";
    }
}
