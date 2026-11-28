package android.graphics;

/** JVM 测试桩：Bitmap（内存像素数组，测试可读回像素解码二维码）。 */
public class Bitmap {

    public enum Config {
        ALPHA_8, RGB_565, ARGB_4444, ARGB_8888
    }

    public enum CompressFormat {
        JPEG, PNG, WEBP
    }

    private final int width;
    private final int height;
    private final int[] pixels;

    private Bitmap(int w, int h) {
        width = w;
        height = h;
        pixels = new int[w * h];
    }

    public static Bitmap createBitmap(int width, int height, Config config) {
        return new Bitmap(width, height);
    }

    public int getWidth() {
        return width;
    }

    public int getHeight() {
        return height;
    }

    public void setPixels(int[] px, int offset, int stride, int x, int y, int w, int h) {
        for (int row = 0; row < h; row++) {
            System.arraycopy(px, offset + row * stride,
                    pixels, (y + row) * width + x, w);
        }
    }

    public void getPixels(int[] px, int offset, int stride, int x, int y, int w, int h) {
        for (int row = 0; row < h; row++) {
            System.arraycopy(pixels, (y + row) * width + x,
                    px, offset + row * stride, w);
        }
    }

    public void recycle() {
    }

    public boolean compress(CompressFormat format, int quality, java.io.OutputStream stream) {
        return false;
    }
}
