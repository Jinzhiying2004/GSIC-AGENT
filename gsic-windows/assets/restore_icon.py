#!/usr/bin/env python3
"""从 base64 内嵌文件还原图标（gsic-icon.png + gsic.ico）。

二进制图标不进版本跟踪（推送管线不支持二进制），以 base64 存在
gsic-icon.b64.json，构建时由本脚本还原。纯标准库，无第三方依赖。
"""
import base64
import json
from pathlib import Path

HERE = Path(__file__).resolve().parent
ROOT = HERE.parent


def main():
    data = json.loads((HERE / "gsic-icon.b64.json").read_text())
    (HERE / "gsic-icon.png").write_bytes(base64.b64decode(data["png"]))
    ico = base64.b64decode(data["ico"])
    (HERE / "gsic.ico").write_bytes(ico)
    (ROOT / "installer" / "gsic.ico").write_bytes(ico)
    print("图标已还原：assets/gsic-icon.png、assets/gsic.ico、installer/gsic.ico")


if __name__ == "__main__":
    main()
