package android.content;

import android.net.Uri;

/** JVM 测试桩：Intent（只含被测代码用到的构造器/方法/常量）。 */
public class Intent {

    public static final String ACTION_VIEW = "android.intent.action.VIEW";
    public static final String ACTION_SECURITY_SETTINGS = "android.settings.SECURITY_SETTINGS";
    public static final int FLAG_GRANT_READ_URI_PERMISSION = 1;
    public static final int FLAG_ACTIVITY_NEW_TASK = 0x10000000;

    public Intent() {
    }

    public Intent(String action) {
    }

    public Intent(String action, Uri uri) {
    }

    public Intent addFlags(int flags) {
        return this;
    }

    public Intent setDataAndType(Uri uri, String type) {
        return this;
    }
}
