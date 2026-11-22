//go:build windows

package main

import (
	"bytes"
	"fmt"
	"image"
	"image/jpeg"
	"unsafe"
)

const (
	smXVirtualScreen  = 76
	smYVirtualScreen  = 77
	smCXVirtualScreen = 78
	smCYVirtualScreen = 79
	srccopy           = 0x00CC0020
	dibRGBColors      = 0
	biRGB             = 0
)

type bitmapInfoHeader struct {
	BiSize          uint32
	BiWidth         int32
	BiHeight        int32
	BiPlanes        uint16
	BiBitCount      uint16
	BiCompression   uint32
	BiSizeImage     uint32
	BiXPelsPerMeter int32
	BiYPelsPerMeter int32
	BiClrUsed       uint32
	BiClrImportant  uint32
}

func getSystemMetrics(n int) int {
	r, _, _ := procGetSystemMetrics.Call(uintptr(n))
	return int(int32(r))
}

// ScreenSize returns the virtual screen size (all monitors).
func ScreenSize() (width, height int) {
	return getSystemMetrics(smCXVirtualScreen), getSystemMetrics(smCYVirtualScreen)
}

// CaptureScreen grabs the whole virtual screen and returns JPEG bytes.
// quality is clamped to 1..100 (default 80).
func CaptureScreen(quality int) (jpegBytes []byte, width, height int, err error) {
	if quality < 1 {
		quality = 1
	}
	if quality > 100 {
		quality = 100
	}
	vx := getSystemMetrics(smXVirtualScreen)
	vy := getSystemMetrics(smYVirtualScreen)
	width = getSystemMetrics(smCXVirtualScreen)
	height = getSystemMetrics(smCYVirtualScreen)
	if width <= 0 || height <= 0 {
		return nil, 0, 0, fmt.Errorf("invalid screen size %dx%d", width, height)
	}

	hdc, _, _ := procGetDC.Call(0)
	if hdc == 0 {
		return nil, 0, 0, fmt.Errorf("GetDC failed")
	}
	defer procReleaseDC.Call(0, hdc)

	memDC, _, _ := procCreateCompatibleDC.Call(hdc)
	if memDC == 0 {
		return nil, 0, 0, fmt.Errorf("CreateCompatibleDC failed")
	}
	defer procDeleteDC.Call(memDC)

	hBmp, _, _ := procCreateCompatibleBitmap.Call(hdc, uintptr(width), uintptr(height))
	if hBmp == 0 {
		return nil, 0, 0, fmt.Errorf("CreateCompatibleBitmap failed")
	}
	defer procDeleteObject.Call(hBmp)

	oldObj, _, _ := procSelectObject.Call(memDC, hBmp)
	defer procSelectObject.Call(memDC, oldObj)

	ok, _, _ := procBitBlt.Call(memDC, 0, 0, uintptr(width), uintptr(height), hdc, uintptr(vx), uintptr(vy), srccopy)
	if ok == 0 {
		return nil, 0, 0, fmt.Errorf("BitBlt failed")
	}

	bi := bitmapInfoHeader{
		BiSize:        uint32(unsafe.Sizeof(bitmapInfoHeader{})),
		BiWidth:       int32(width),
		BiHeight:      -int32(height), // top-down DIB
		BiPlanes:      1,
		BiBitCount:    32,
		BiCompression: biRGB,
	}
	pixels := make([]byte, width*height*4)
	lines, _, _ := procGetDIBits.Call(memDC, hBmp, 0, uintptr(height),
		uintptr(unsafe.Pointer(&pixels[0])), uintptr(unsafe.Pointer(&bi)), dibRGBColors)
	if lines == 0 {
		return nil, 0, 0, fmt.Errorf("GetDIBits failed")
	}

	// BGRA -> RGBA.
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	for i := 0; i < width*height; i++ {
		src := pixels[i*4:]
		dst := img.Pix[i*4:]
		dst[0] = src[2] // R
		dst[1] = src[1] // G
		dst[2] = src[0] // B
		dst[3] = 0xFF   // A
	}

	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: quality}); err != nil {
		return nil, 0, 0, fmt.Errorf("jpeg encode: %w", err)
	}
	return buf.Bytes(), width, height, nil
}
