//go:build windows

package main

import (
	"bytes"
	"fmt"
	"image/png"

	"github.com/lxn/walk"
	"github.com/skip2/go-qrcode"
)

// qrConnectText builds the QR payload: connection info plus the
// instruction to fetch the full agent prompt from the device.
// The full prompt (~4KB) is too big for a QR code, so the code
// carries only address + token and points at /agent-prompt.
func qrConnectText(tailIP string, port int, token string) string {
	return fmt.Sprintf("GSIC 远控连接\n地址：http://%s:%d\nToken：%s\n连上后先用 X-Token 请求头调 GET /agent-prompt 取完整操作说明，再开始工作。",
		tailIP, port, token)
}

// qrBitmap renders the connect QR code as a Walk bitmap (size px square).
func qrBitmap(tailIP string, port int, token string, size int) (*walk.Bitmap, error) {
	qr, err := qrcode.New(qrConnectText(tailIP, port, token), qrcode.Medium)
	if err != nil {
		return nil, err
	}
	pngBytes, err := qr.PNG(size)
	if err != nil {
		return nil, err
	}
	img, err := png.Decode(bytes.NewReader(pngBytes))
	if err != nil {
		return nil, err
	}
	return walk.NewBitmapFromImage(img)
}
