#!/bin/bash
# JVM 单元测试（Android stub，不用真机）。
# 用法：bash tests/run_tests.sh（在 phone-agent-apk 目录下）
set -e
cd "$(dirname "$0")/.."
export JAVA_HOME=/home/hatch/build-tools/jdk17
export PATH=$JAVA_HOME/bin:$PATH
SDK=/home/hatch/android-sdk
ANDROID_JAR=$SDK/platforms/android-34/android.jar
ZXING=libs/core-3.5.3.jar
# org.json：android.jar 里的是空桩，测试用 Maven Central 真实实现
JSON_JAR=tests/libs/json-20240303.jar
OUT=out/tests
rm -rf $OUT && mkdir -p $OUT/stubs $OUT/prod $OUT/test

echo "== 编译 stubs =="
javac -encoding UTF-8 -source 8 -target 8 -nowarn -cp "$ANDROID_JAR" \
  -d $OUT/stubs $(find tests/stubs -name "*.java")

echo "== 编译 production（测试子集） =="
javac -encoding UTF-8 -source 8 -target 8 -nowarn \
  -cp "$OUT/stubs:$JSON_JAR:$ANDROID_JAR:$ZXING" -d $OUT/prod \
  src/com/gsic/app/TokenManager.java src/com/gsic/app/SecurePrefs.java \
  src/com/gsic/app/PairingManager.java src/com/gsic/app/AgentPrompt.java \
  src/com/gsic/app/UpdateManager.java

echo "== 编译 tests =="
javac -encoding UTF-8 -source 8 -target 8 -nowarn \
  -cp "$OUT/stubs:$OUT/prod:$JSON_JAR:$ANDROID_JAR:$ZXING" -d $OUT/test \
  $(find tests/src -name "*.java")

echo "== 运行 =="
FAIL=0
for T in TestPairing TestTokenDefault TestUpdateSha TestAgentPrompt; do
  echo "--- $T ---"
  java -Dfile.encoding=UTF-8 -cp "$OUT/stubs:$OUT/prod:$OUT/test:$JSON_JAR:$ANDROID_JAR:$ZXING" test.$T || FAIL=1
done
if [ "$FAIL" = 0 ]; then
  echo "全部测试通过"
else
  echo "有测试失败"
  exit 1
fi
