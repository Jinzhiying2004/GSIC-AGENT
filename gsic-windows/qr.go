//go:build windows

package main

import (
	"bytes"
	"image/png"

	"github.com/lxn/walk"
	"github.com/skip2/go-qrcode"
)

// qrConnectText lives in pairing.go: the QR payload carries only the
// address + one-time pairing code, never any credential (Issue #3).

// qrBitmap renders the connect QR code as a Walk bitmap (size px square).
func qrBitmap(tailIP string, port int, pairingCode string, size int) (*walk.Bitmap, error) {
	qr, err := qrcode.New(qrConnectText(tailIP, port, pairingCode), qrcode.Medium)
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
