package com.gsic.app;

import org.json.JSONArray;
import org.json.JSONObject;

import java.util.ArrayList;
import java.util.Collections;
import java.util.List;

/**
 * 远控步骤任务（/task steps 模式）的实时状态。
 *
 * <p>手机端 TaskRunner 跑在后台线程，执行日志经 Listener
 * 写入这里；云端用 {@code GET /task/status} 轮询，App 内远控页用卡片展示，
 * 做到"边跑边看"，不再是跑完才一次性返回。</p>
 *
 * <p>goal 模式已移除：不再有思考过程与低置信澄清；
 * 步数从"步骤 N/M"日志解析，总步数由下发方在构造时传入。</p>
 *
 * <p>纯 Java + org.json，可独立 JVM 单测（step 解析逻辑）。</p>
 */
public class RemoteTask {

    private static int seq = 0;

    public final String taskId;
    /** 任务名（steps 任务的 name 字段，下发方传入） */
    public final String name;
    /** 总步数（steps 数组长度，下发方传入；未知时传 0） */
    public final int totalSteps;
    public final long startTime = System.currentTimeMillis();
    public final List<String> logs =
            Collections.synchronizedList(new ArrayList<String>());
    public volatile boolean done = false;
    public volatile boolean ok = false;
    public volatile String error = "";
    /** v3.21：远程停止请求（/task/stop 置位，异步执行方需据此调 TaskRunner.stop()） */
    public volatile boolean stopRequested = false;
    public volatile JSONArray audit = null;

    public RemoteTask(String name, int totalSteps) {
        this.taskId = "rt-" + (++seq) + "-" + (startTime % 100000);
        this.name = name == null ? "" : name;
        this.totalSteps = totalSteps;
    }

    public void log(String m) {
        if (m != null) logs.add(m);
    }

    /** 当前步数：从最近一条"步骤 N/M: ..."日志解析；无则为 0 */
    public int step() {
        synchronized (logs) {
            for (int i = logs.size() - 1; i >= 0; i--) {
                String l = logs.get(i);
                if (!l.startsWith("步骤 ")) continue;
                int slash = l.indexOf('/');
                if (slash <= 3) continue;
                try {
                    return Integer.parseInt(l.substring(3, slash).trim());
                } catch (Exception ignored) {
                }
            }
        }
        return 0;
    }

    /** 最近一条日志（当前状态）；无则为空 */
    public String lastLog() {
        synchronized (logs) {
            if (logs.isEmpty()) return "";
            return logs.get(logs.size() - 1);
        }
    }

    /** 最近 n 条日志（按时间顺序） */
    public List<String> tail(int n) {
        synchronized (logs) {
            int from = Math.max(0, logs.size() - n);
            return new ArrayList<>(logs.subList(from, logs.size()));
        }
    }

    public long elapsedSec() {
        return (System.currentTimeMillis() - startTime) / 1000;
    }

    /** 供 /task/status 与 App 内卡片共用的 JSON */
    public JSONObject toJson() {
        JSONObject o = new JSONObject();
        try {
            o.put("ok", true);
            o.put("taskId", taskId);
            o.put("name", name);
            o.put("totalSteps", totalSteps);
            o.put("done", done);
            o.put("resultOk", ok);
            if (!error.isEmpty()) o.put("error", error);
            o.put("step", step());
            o.put("elapsedSec", elapsedSec());
            JSONArray arr = new JSONArray();
            for (String l : tail(25)) arr.put(l);
            o.put("log", arr);
            if (audit != null) o.put("audit", audit);
        } catch (Exception ignored) {
        }
        return o;
    }
}
