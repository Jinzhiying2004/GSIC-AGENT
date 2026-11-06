package com.gsic.app;

import android.content.Context;
import android.graphics.PixelFormat;
import android.os.Build;
import android.os.Handler;
import android.os.Looper;
import android.view.Gravity;
import android.view.WindowManager;
import android.widget.FrameLayout;

/**
 * v4.18 锁屏章鱼待机（用户 11-04 亲定“开机自启后，桌面八爪鱼待机
 * 也要在锁屏显示”）：服务持有的全屏悬浮层，黑底 + PetView 章鱼，
 * 与 App 内横屏章鱼待机（MainActivity.enterStandby）同款显示、
 * 不同宿主——App 内版只在 GSIC 横屏主页 resumed 时存在，锁屏版
 * 由 AgentService 在锁屏亮屏时挂出，GSIC 不在前台也能显示。
 * v4.20 起默认关（用户 11-06 令）：ui.lock_standby 默认 false，锁屏亮屏不再自动挂出，设置里手动打开仍可恢复。
 *
 * 关键边界，一律不碰解锁安全：
 * - 只用 FLAG_SHOW_WHEN_LOCKED 盖在锁屏上面显示，绝不用
 *   FLAG_DISMISS_KEYGUARD、不解任何锁，解锁仍走系统锁屏；
 * - 触摸全穿透（FLAG_NOT_TOUCHABLE）：锁屏滑动解锁、输密码、
 *   人脸解锁都不被挡，章鱼只是显示，不吃任何一次触摸；
 * - 不分横竖屏：锁屏通常是竖屏，竖屏也显示（App 内版只横屏，
 *   两者不要混）。
 *
 * 收起条件（任一即收，防盖住正常界面与智能体干活）：
 * - 屏幕灭（SCREEN_OFF）、用户解锁（USER_PRESENT）；
 * - 任何任务开始（FloatWindow.beginTask 统一调 hide）——否则
 *   主屏任务的画面会被黑屏盖住、模型拍到的也是黑屏；
 * - 虚拟屏在跑、悬浮窗权限没给、设置里关了锁屏待机时不挂出。
 */
public class LockStandby {

    private static WindowManager wm;
    private static FrameLayout lockView;
    private static PetView lockPet;
    private static final Handler mainHandler =
            new Handler(Looper.getMainLooper());

    public static boolean isShowing() {
        return lockView != null;
    }

    /** 锁屏亮屏时的挂出判断：全部门都过才显示，任一不过就静默不挂。 */
    public static void maybeShow(Context ctx) {
        try {
            Context ac = ctx.getApplicationContext();
            if (lockView != null) return;
            if (!FloatWindow.hasPermission(ac)) return;
            if (!SettingRegistry.getBool(ac, "ui.lock_standby", false)) return;
            if (FloatWindow.isTaskActive()) return;
            if (VirtualDisplayManager.isActive()) return;
            android.app.KeyguardManager km =
                    (android.app.KeyguardManager) ac.getSystemService(
                            Context.KEYGUARD_SERVICE);
            if (km == null || !km.isKeyguardLocked()) return;
            show(ac);
        } catch (Exception ignored) {
        }
    }

    public static void show(Context ctx) {
        if (Looper.myLooper() != Looper.getMainLooper()) {
            mainHandler.post(() -> show(ctx));
            return;
        }
        try {
            Context ac = ctx.getApplicationContext();
            if (lockView != null) return;
            if (!FloatWindow.hasPermission(ac)) return;
            hideLocked();
            wm = (WindowManager) ac.getSystemService(Context.WINDOW_SERVICE);
            FrameLayout black = new FrameLayout(ac);
            black.setBackgroundColor(android.graphics.Color.BLACK);
            PetView pet = new PetView(ac);
            black.addView(pet, new FrameLayout.LayoutParams(
                    FrameLayout.LayoutParams.MATCH_PARENT,
                    FrameLayout.LayoutParams.MATCH_PARENT));
            int type = Build.VERSION.SDK_INT >= 26
                    ? WindowManager.LayoutParams.TYPE_APPLICATION_OVERLAY
                    : WindowManager.LayoutParams.TYPE_PHONE;
            WindowManager.LayoutParams params = new WindowManager.LayoutParams(
                    WindowManager.LayoutParams.MATCH_PARENT,
                    WindowManager.LayoutParams.MATCH_PARENT,
                    type,
                    WindowManager.LayoutParams.FLAG_NOT_FOCUSABLE
                            | WindowManager.LayoutParams.FLAG_NOT_TOUCHABLE
                            | WindowManager.LayoutParams.FLAG_NOT_TOUCH_MODAL
                            | WindowManager.LayoutParams.FLAG_LAYOUT_IN_SCREEN
                            | WindowManager.LayoutParams.FLAG_SHOW_WHEN_LOCKED,
                    PixelFormat.TRANSLUCENT);
            params.gravity = Gravity.TOP | Gravity.START;
            wm.addView(black, params);
            lockView = black;
            lockPet = pet;
            pet.startAnim();
        } catch (Exception ignored) {
            lockView = null;
            lockPet = null;
        }
    }

    public static void hide() {
        if (Looper.myLooper() != Looper.getMainLooper()) {
            mainHandler.post(LockStandby::hide);
            return;
        }
        hideLocked();
    }

    private static void hideLocked() {
        try {
            if (lockPet != null) lockPet.stopAnim();
        } catch (Exception ignored) {
        }
        try {
            if (lockView != null && wm != null) wm.removeView(lockView);
        } catch (Exception ignored) {
        }
        lockView = null;
        lockPet = null;
    }
}
