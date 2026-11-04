package com.gsic.app;

import java.util.ArrayDeque;
import java.util.Deque;

/**
 * v3.83 第三批：工具调用防环检测器（只做检查层，不动主循环）。
 * 记录（动作名、参数 hash、屏 hash）的滑动窗口；同一签名连续重复达到
 * 警告阈值 → WARN（调用方跳过本次并强制换策略），达到熔断阈值 → FUSE
 * （调用方熔断停任务并报疑似死循环）。阈值可调（默认 3 / 5）。
 * 纯 Java，可在桌面 JVM 单测。
 */
public class LoopDetector {

    public enum Verdict { OK, WARN, FUSE }

    private final int warnThreshold;
    private final int fuseThreshold;
    private final Deque<String> window = new ArrayDeque<>();
    private static final int WINDOW_CAP = 24;
    private String lastSig = null;
    private int streak = 0;

    public LoopDetector(int warnThreshold, int fuseThreshold) {
        this.warnThreshold = Math.max(1, warnThreshold);
        this.fuseThreshold = Math.max(this.warnThreshold + 1, fuseThreshold);
    }

    public int getWarnThreshold() { return warnThreshold; }
    public int getFuseThreshold() { return fuseThreshold; }

    /** 记录一次动作，返回本轮裁决；签名 = 动作名|参数（去空白）|屏 hash。 */
    public synchronized Verdict record(String action, String argsJson, String screenHash) {
        String sig = (action == null ? "" : action) + "|"
                + (argsJson == null ? "" : argsJson.replaceAll("\\s+", "")) + "|"
                + (screenHash == null ? "" : screenHash);
        window.addLast(sig);
        while (window.size() > WINDOW_CAP) window.removeFirst();
        if (sig.equals(lastSig)) {
            streak++;
        } else {
            lastSig = sig;
            streak = 1;
        }
        if (streak >= fuseThreshold) return Verdict.FUSE;
        if (streak >= warnThreshold) return Verdict.WARN;
        return Verdict.OK;
    }

    /** 当前连续重复次数（供日志）。 */
    public synchronized int getStreak() { return streak; }

    public synchronized void reset() {
        window.clear();
        lastSig = null;
        streak = 0;
    }
}
