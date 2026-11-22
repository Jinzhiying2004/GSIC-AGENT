package com.gsic.app;

import android.app.Notification;
import android.app.NotificationChannel;
import android.app.NotificationManager;
import android.app.Service;
import android.content.Context;
import android.content.Intent;
import android.os.Build;
import android.os.IBinder;

/** 前台服务：保活 HTTP 控制服务。端口可在 App 内修改（默认 28789），token 鉴权。 */
public class AgentService extends Service {

    /** 默认端口（v2.41 起由 18789 改为 28789） */
    public static final int DEFAULT_PORT = 28789;

    /** 当前生效端口（用户可在远控页修改） */
    public static int getPort(Context ctx) {
        return ctx.getSharedPreferences("svc", Context.MODE_PRIVATE)
                .getInt("port", DEFAULT_PORT);
    }

    public static void setPort(Context ctx, int port) {
        ctx.getSharedPreferences("svc", Context.MODE_PRIVATE)
                .edit().putInt("port", port).apply();
    }

    private static volatile boolean running = false;

    // v3.05：静态控制 TG（供 MainActivity 调用）
    private static AgentService instance = null;
    // v4.18 锁屏章鱼待机：服务内登记亮屏/灭屏/解锁广播——锁屏亮屏
    // 时挂黑屏章鱼，灭屏或解锁即收起（USER_PRESENT 是系统在解锁
    // 成功后发的广播，不用 GSIC 自己猜锁屏状态）。
    private android.content.BroadcastReceiver lockStandbyReceiver;

    public static boolean isRunning() {
        return running;
    }

    private AgentHttpServer http;
    private PetController pet;
    private android.os.Handler schedHandler;
    // v4.22：6 小时本体定时重启——服务启动时间戳，schedTick 每分钟
    // 检查一次，满 6 小时且无任务在跑时重启进程（root 看门狗拉回）。
    private long serviceStartAt = 0;
    private static final long RESTART_INTERVAL_MS = 6L * 3600_000L;

    private final Runnable schedTick = new Runnable() {
        @Override
        public void run() {
            try {
                runDueSchedules();
            } catch (Exception ignored) {
            }
            try {
                maybeRestartApp();
            } catch (Exception ignored) {
            }
            if (schedHandler != null) schedHandler.postDelayed(this, 60_000);
        }
    };

    /** v4.22：6 小时定时重启——满 6 小时且无任务在跑时重启本体。
     * 有任务在跑时跳过本轮，下分钟再查（任务结束才重启，不中断任务）。 */
    private void maybeRestartApp() {
        if (serviceStartAt <= 0) return;
        long uptime = System.currentTimeMillis() - serviceStartAt;
        if (uptime < RESTART_INTERVAL_MS) return;
        if (FloatWindow.isTaskActive()) return;
        restartApp();
    }

    /** v4.22：本体重启——记日志、停 tsnet/CF、停服务、2 秒后杀进程。
     * 进程死后由 Magisk root 看门狗（60 秒巡检）+ START_STICKY 拉回服务。 */
    private void restartApp() {
        try {
            long now = System.currentTimeMillis();
            getSharedPreferences("agent", MODE_PRIVATE)
                    .edit().putLong("last_auto_restart_at", now).apply();
            android.util.Log.i("AgentService",
                    "6 小时定时重启：uptime=" + (now - serviceStartAt) + "ms");
        } catch (Exception ignored) {
        }
        try {
            TsnetNode.stopProcessOnly();
        } catch (Exception ignored) {
        }
        try {
            CloudflareTunnel.stopProcessOnly();
        } catch (Exception ignored) {
        }
        try {
            stopSelf();
        } catch (Exception ignored) {
        }
        try {
            new android.os.Handler(android.os.Looper.getMainLooper())
                    .postDelayed(() -> {
                        android.os.Process.killProcess(
                                android.os.Process.myPid());
                    }, 2000);
        } catch (Exception ignored) {
        }
    }

    /** 本地定时任务：每天 / 每间隔执行技能，零 token */
    private void runDueSchedules() {
        ScheduleStore store = new ScheduleStore(this);
        SkillStore skills = new SkillStore(this);
        long now = System.currentTimeMillis();
        for (ScheduleStore.Schedule sc : store.due(now)) {
            String json = null;
            String name = sc.skillName;
            for (SkillStore.Skill sk : skills.list()) {
                if (sk.id.equals(sc.skillId) && sk.enabled) {
                    json = sk.json;
                    name = sk.name;
                    break;
                }
            }
            boolean ok = false;
            if (json != null) {
                TaskRunner r = new TaskRunner(this, msg -> {
                });
                ok = r.run(json);
            }
            store.touch(sc.id, now);
        }
    }

    @Override
    public void onCreate() {
        super.onCreate();
        instance = this;
        // v4.22：6 小时定时重启计时起点（服务每次启动重置）
        serviceStartAt = System.currentTimeMillis();
        // v3.52：开机自启回执确认（有待确认的开机广播则落 boot_ok_ts）
        BootReceiver.noteServiceUp(this);
        // 地基·自更新：装后自验对账 + 启动静默检查（24h 节流、只记不扰）
        UpdateManager.confirmIfUpdated(this);
        UpdateManager.silentCheck(this);
        // v3.54 热更新诊断 E1：运行中 dump ClassLoader 元素构成+补丁类 CodeSource
        // （异步，不阻塞启动；与 attach 时刻 dump 对比即 E3）
        PatchDiag.dumpAsync(this, "service");
        String chId = "agent_svc";
        if (Build.VERSION.SDK_INT >= 26) {
            NotificationChannel ch = new NotificationChannel(
                    chId, "远程控制服务", NotificationManager.IMPORTANCE_LOW);
            ((NotificationManager) getSystemService(NOTIFICATION_SERVICE))
                    .createNotificationChannel(ch);
        }
        Notification.Builder nb = Build.VERSION.SDK_INT >= 26
                ? new Notification.Builder(this, chId)
                : new Notification.Builder(this);
        int port = getPort(this);
        Notification n = nb.setContentTitle("GSIC")
                .setContentText("远程控制服务运行中（端口 " + port + "）")
                .setSmallIcon(android.R.drawable.ic_dialog_info)
                .build();
        startForeground(1, n);
        try {
            http = new AgentHttpServer(port, new ApiHandler(this));
            http.start();
            running = true;
            // 定时任务调度循环（每分钟检查一次）
            schedHandler = new android.os.Handler(android.os.Looper.getMainLooper());
            schedHandler.postDelayed(schedTick, 30_000);
            // v3.42 常驻三层：闹钟看门狗 + Magisk root 守护（开机自启在 BootReceiver）
            scheduleWatchdog();
            installRootWatchdog();
            // v3.91：启动优先级——内置节点优先；只有内置没开时，CF 隧道才按开关恢复。
            // （手动启动 CF 不受影响；运行中内置断了再起 CF 属于运行时 fallback，另算。）
            try {
                TsnetNode.startIfEnabled(this, port);
            } catch (Exception ignored) {
            }
            try {
                if (!TsnetNode.isEnabled(this)) {
                    CloudflareTunnel.startIfEnabled(this, port);
                }
            } catch (Exception ignored) {
            }
            // v3.48：待机光效「M 八爪鱼」
            try {
                pet = new PetController(this);
                pet.start();
            } catch (Exception ignored) {
            }
            // v4.18 锁屏章鱼待机：服务起来即登记，之后每次锁屏亮屏
            // 都能挂出（开机自启后、GSIC 不在前台时同样生效）。
            try {
                lockStandbyReceiver = new android.content.BroadcastReceiver() {
                    @Override
                    public void onReceive(android.content.Context c,
                            android.content.Intent intent) {
                        String a = intent == null ? "" : intent.getAction();
                        if (android.content.Intent.ACTION_SCREEN_ON.equals(a)) {
                            LockStandby.maybeShow(c);
                        } else if (android.content.Intent.ACTION_SCREEN_OFF.equals(a)
                                || android.content.Intent.ACTION_USER_PRESENT.equals(a)) {
                            LockStandby.hide();
                        }
                    }
                };
                android.content.IntentFilter f =
                        new android.content.IntentFilter();
                f.addAction(android.content.Intent.ACTION_SCREEN_ON);
                f.addAction(android.content.Intent.ACTION_SCREEN_OFF);
                f.addAction(android.content.Intent.ACTION_USER_PRESENT);
                registerReceiver(lockStandbyReceiver, f);
                // 服务启动时若屏已亮且正锁着（例如自启后第一次亮屏），
                // 不等下一次 SCREEN_ON，直接判断一次。
                LockStandby.maybeShow(this);
            } catch (Exception ignored) {
            }
        } catch (Exception e) {
            stopSelf();
        }
    }

    /** v3.42：闹钟看门狗——每 10 分钟叫醒 BootReceiver 检查服务是否还活着 */
    private void scheduleWatchdog() {
        try {
            android.app.AlarmManager am = (android.app.AlarmManager)
                    getSystemService(ALARM_SERVICE);
            Intent i = new Intent(this, BootReceiver.class);
            i.setAction("com.gsic.app.WATCHDOG");
            android.app.PendingIntent pi = android.app.PendingIntent.getBroadcast(
                    this, 421, i,
                    android.app.PendingIntent.FLAG_UPDATE_CURRENT);
            long interval = 10 * 60_000L;
            am.setRepeating(android.app.AlarmManager.RTC_WAKEUP,
                    System.currentTimeMillis() + interval, interval, pi);
        } catch (Exception ignored) {
        }
    }

    // v3.42 root 守护脚本（Magisk service.d 开机执行，不依赖 MIUI 自启动放行）
    private static final String WATCHDOG_SH =
            "#!/system/bin/sh\n"
            + "# gsic 常驻守护：服务进程不在就拉起；"
            + "用户手动停止（svc_off 标记）时不拉起\n"
            + "sleep 40\n"
            + "while true; do\n"
            + "  if [ ! -f /data/data/com.gsic.app/files/svc_off ]; then\n"
            + "    pidof com.gsic.app >/dev/null 2>&1 || "
            + "am startservice -n com.gsic.app/com.gsic.app.AgentService "
            + ">/dev/null 2>&1\n"
            + "  fi\n"
            + "  sleep 60\n"
            + "done\n";

    /** v3.42：安装 Magisk root 守护（每版本装一次；无 root/无 Magisk 静默跳过） */
    private void installRootWatchdog() {
        try {
            android.content.SharedPreferences sp =
                    getSharedPreferences("agent", MODE_PRIVATE);
            if (sp.getInt("wd_installed_ver", 0) >= 42) return;
            sp.edit().putInt("wd_installed_ver", 42).apply();
            new Thread(() -> {
                try {
                    if (Root.findSu() == null) return;
                    java.io.File tmp = new java.io.File(
                            getFilesDir(), "gsic_watchdog.sh");
                    java.io.FileOutputStream fos =
                            new java.io.FileOutputStream(tmp);
                    fos.write(WATCHDOG_SH.getBytes("UTF-8"));
                    fos.close();
                    String cmd = "mkdir -p /data/adb/service.d"
                            + " && cp " + tmp.getAbsolutePath()
                            + " /data/adb/service.d/gsic_watchdog.sh"
                            + " && chmod 755 /data/adb/service.d/gsic_watchdog.sh"
                            + "; dumpsys deviceidle whitelist +com.gsic.app"
                            + "; pgrep -f gsic_watchdog >/dev/null"
                            + " || (setsid sh /data/adb/service.d/gsic_watchdog.sh"
                            + " >/dev/null 2>&1 &)";
                    RootShell.get().exec(cmd, 30);
                } catch (Exception ignored) {
                }
            }, "wd-install").start();
        } catch (Exception ignored) {
        }
    }

    @Override
    public int onStartCommand(Intent intent, int flags, int startId) {
        return START_STICKY;
    }

    /**
     * v3.22-hotfix：用户划掉任务卡片时自重启。
     * MIUI 下从最近任务划掉会杀进程，靠 START_STICKY 常拉不起来，
     * 这里主动再拉一次。
     */
    @Override
    public void onTaskRemoved(android.content.Intent rootIntent) {
        try {
            android.content.Intent i = new android.content.Intent(
                    getApplicationContext(), AgentService.class);
            if (android.os.Build.VERSION.SDK_INT >= 26) {
                getApplicationContext().startForegroundService(i);
            } else {
                startService(i);
            }
        } catch (Exception ignored) {
        }
        super.onTaskRemoved(rootIntent);
    }

    @Override
    public void onDestroy() {
        running = false;
        // v4.18 锁屏章鱼待机：服务销毁时注销广播并收起锁屏层，
        // 免得服务没了黑屏还盖在锁屏上。
        try {
            if (lockStandbyReceiver != null) {
                unregisterReceiver(lockStandbyReceiver);
                lockStandbyReceiver = null;
            }
        } catch (Exception ignored) {
        }
        try {
            LockStandby.hide();
        } catch (Exception ignored) {
        }
        // v3.67：服务销毁时停隧道进程，开关保留，下次服务启动按开关恢复
        try {
            CloudflareTunnel.stopProcessOnly();
        } catch (Exception ignored) {
        }
        // v3.82：服务销毁时优雅停 tsnet 进程，开关保留
        try {
            TsnetNode.stopProcessOnly();
        } catch (Exception ignored) {
        }
        if (pet != null) pet.stop();
        instance = null;
        if (schedHandler != null) schedHandler.removeCallbacks(schedTick);
        if (http != null) http.stop();
        super.onDestroy();
    }

    @Override
    public IBinder onBind(Intent intent) {
        return null;
    }
}
