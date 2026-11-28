package android.content.res;

import java.io.IOException;
import java.io.InputStream;

/** JVM 测试桩：AssetManager（FakeAssetManager 重写 open）。 */
public class AssetManager {
    public InputStream open(String fileName) throws IOException {
        throw new IOException("stub AssetManager");
    }
}
