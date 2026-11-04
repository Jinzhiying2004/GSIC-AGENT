package com.gsic.app;

import java.text.SimpleDateFormat;
import java.util.ArrayList;
import java.util.Date;
import java.util.List;
import java.util.Locale;

/**
 * v4.04 横屏主页右侧“智能体工具与代码”共享日志。
 * 来源：AgentLoop 每次工具执行完（PhoneTools.execute 返回后），
 * 记工具名 + 参数代码（argsJson）+ 成败与结果摘要。
 * 与顶部蓝条相反：这里就是要看代码，参数原样保留（截断防爆）。
 * 只保留最近 100 条，三端共用同一份（App / 远控 / TG 的 AgentLoop
 * 都走这里），横屏主页的 800ms 对表从这里读。
 */
public class AgentToolLog {

    private static final int MAX = 100;
    private static final List<String> entries = new ArrayList<>();
    private static long version = 0;

    public static synchronized void add(String name, String argsJson,
            boolean ok, String result) {
        try {
            String time = new SimpleDateFormat("HH:mm:ss",
                    Locale.getDefault()).format(new Date());
            String args = argsJson == null ? "{}" : argsJson;
            if (args.length() > 1000) args = args.substring(0, 1000) + "…";
            String res = result == null ? "" : result.replace("\n", " ").trim();
            if (res.length() > 200) res = res.substring(0, 200) + "…";
            StringBuilder sb = new StringBuilder();
            sb.append(time).append("  ").append(name)
                    .append(ok ? "  ✓" : "  ✗").append("\n");
            sb.append(args).append("\n");
            if (!res.isEmpty()) sb.append("→ ").append(res).append("\n");
            sb.append("────────────────\n");
            entries.add(sb.toString());
            while (entries.size() > MAX) entries.remove(0);
            version++;
        } catch (Exception ignored) {}
    }

    public static synchronized String getText() {
        StringBuilder sb = new StringBuilder();
        for (String e : entries) sb.append(e);
        return sb.toString();
    }

    public static synchronized long getVersion() {
        return version;
    }

    public static synchronized boolean isEmpty() {
        return entries.isEmpty();
    }
}
