package test;

import android.content.ContentResolver;
import android.content.Context;
import android.content.SharedPreferences;
import android.content.pm.PackageManager;
import android.content.res.AssetManager;

import java.io.File;
import java.util.HashMap;
import java.util.Map;

/** 内存 Context（JVM 测试用）。 */
public class FakeContext extends Context {

    private final Map<String, FakeSharedPreferences> prefs = new HashMap<>();
    private final FakeAssetManager assets = new FakeAssetManager();
    private final ContentResolver resolver = new ContentResolver();
    private final File filesDir;

    public FakeContext() {
        filesDir = new File(System.getProperty("java.io.tmpdir"),
                "fake-ctx-" + System.nanoTime());
        filesDir.mkdirs();
    }

    @Override
    public Context getApplicationContext() {
        return this;
    }

    @Override
    public SharedPreferences getSharedPreferences(String name, int mode) {
        return prefs.computeIfAbsent(name, k -> new FakeSharedPreferences());
    }

    @Override
    public AssetManager getAssets() {
        return assets;
    }

    @Override
    public ContentResolver getContentResolver() {
        return resolver;
    }

    @Override
    public File getFilesDir() {
        return filesDir;
    }

    @Override
    public File getCacheDir() {
        return filesDir;
    }

    @Override
    public PackageManager getPackageManager() {
        return new PackageManager();
    }

    @Override
    public String getPackageName() {
        return "com.gsic.app";
    }

    @Override
    public Object getSystemService(String name) {
        return null;
    }

    public FakeAssetManager fakeAssets() {
        return assets;
    }

    /** 取指定偏好文件的底层 map（测试直接改 token_time 等字段用）。 */
    public FakeSharedPreferences prefFile(String name) {
        return (FakeSharedPreferences) getSharedPreferences(name, 0);
    }
}
