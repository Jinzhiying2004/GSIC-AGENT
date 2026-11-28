package test;

import android.content.res.AssetManager;

import java.io.ByteArrayInputStream;
import java.io.File;
import java.io.FileNotFoundException;
import java.io.IOException;
import java.io.InputStream;
import java.nio.file.Files;
import java.util.HashMap;
import java.util.Map;

/** 内存 AssetManager（测试往里塞文件字节）。 */
public class FakeAssetManager extends AssetManager {

    private final Map<String, byte[]> files = new HashMap<>();

    public void addFile(String name, byte[] data) {
        files.put(name, data);
    }

    public void addFileFromDisk(String name, File f) throws IOException {
        files.put(name, Files.readAllBytes(f.toPath()));
    }

    @Override
    public InputStream open(String fileName) throws IOException {
        byte[] d = files.get(fileName);
        if (d == null) throw new FileNotFoundException(fileName);
        return new ByteArrayInputStream(d);
    }
}
