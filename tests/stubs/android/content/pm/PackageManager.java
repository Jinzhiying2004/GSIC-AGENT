package android.content.pm;

/** JVM 测试桩：PackageManager（只含被测代码用到的方法）。 */
public class PackageManager {

    public static class NameNotFoundException extends Exception {
        public NameNotFoundException() {
            super("stub");
        }
    }

    public PackageInfo getPackageInfo(String packageName, int flags)
            throws NameNotFoundException {
        throw new NameNotFoundException();
    }

    public PackageInfo getPackageArchiveInfo(String archiveFilePath, int flags) {
        return null;
    }

    public boolean canRequestPackageInstalls() {
        return false;
    }
}
