package android.provider;

import android.content.ContentResolver;

/** JVM 测试桩：Settings（只含被测代码用到的部分）。 */
public class Settings {

    public static final String ACTION_MANAGE_UNKNOWN_APP_SOURCES =
            "android.settings.MANAGE_UNKNOWN_APP_SOURCES";
    public static final String ACTION_SECURITY_SETTINGS =
            "android.settings.SECURITY_SETTINGS";

    public static final class Secure {
        public static final String ANDROID_ID = "android_id";

        /** 测试固定设备指纹。 */
        public static String getString(ContentResolver cr, String name) {
            return "test-android-id-1234";
        }
    }
}
