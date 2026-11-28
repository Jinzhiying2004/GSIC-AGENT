package android.content;

import android.content.pm.PackageManager;
import android.content.res.AssetManager;

/**
 * JVM 测试桩：最小 Context（只含被测 production 代码用到的方法）。
 * 生产 Context 是抽象类；这里用具体类，FakeContext 继承并重写。
 */
public class Context {

    public static final int MODE_PRIVATE = 0;
    public static final int MODE_APPEND = 32768;

    public Context getApplicationContext() {
        return this;
    }

    public SharedPreferences getSharedPreferences(String name, int mode) {
        return null;
    }

    public AssetManager getAssets() {
        return null;
    }

    public ContentResolver getContentResolver() {
        return null;
    }

    public java.io.File getFilesDir() {
        return null;
    }

    public java.io.File getCacheDir() {
        return null;
    }

    public PackageManager getPackageManager() {
        return null;
    }

    public String getPackageName() {
        return null;
    }

    public Object getSystemService(String name) {
        return null;
    }

    public void startActivity(Intent intent) {
    }
}
