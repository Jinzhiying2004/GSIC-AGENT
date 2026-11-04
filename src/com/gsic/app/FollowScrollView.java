package com.gsic.app;

import android.content.Context;
import android.widget.ScrollView;

/**
 * v3.83 第一批：聊天跟随滚动容器。
 * onScrollChanged 实时记录是否在底部：用户上滑离开底部即停跟随，
 * 用户自己滑回底部即恢复跟随；程序触发的 fullScroll 到底后同样回到跟随态。
 */
public class FollowScrollView extends ScrollView {

    private volatile boolean atBottom = true;

    public FollowScrollView(Context ctx) {
        super(ctx);
    }

    /** 当前是否在底部（120px 容差内都算到底）。 */
    public boolean isAtBottom() {
        return atBottom;
    }

    @Override
    protected void onScrollChanged(int l, int t, int oldl, int oldt) {
        super.onScrollChanged(l, t, oldl, oldt);
        try {
            if (getChildCount() == 0) {
                atBottom = true;
                return;
            }
            int contentH = getChildAt(0).getHeight();
            atBottom = (t + getHeight() >= contentH - 120);
        } catch (Exception ignored) {
        }
    }
}
