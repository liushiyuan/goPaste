package goPaste

import (
	"bytes"
	"encoding/binary"

	"image"
	"image/png"
	"runtime"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/image/bmp"
)

const (
	cfBITMAP      = 2
	cfUnicodetext = 13
	gmemMoveable  = 0x0002
)

var (
	user32                     = syscall.MustLoadDLL("user32")
	isClipboardFormatAvailable = user32.MustFindProc("IsClipboardFormatAvailable")
	openClipboard              = user32.MustFindProc("OpenClipboard")
	closeClipboard             = user32.MustFindProc("CloseClipboard")
	emptyClipboard             = user32.MustFindProc("EmptyClipboard")
	getClipboardData           = user32.MustFindProc("GetClipboardData")
	setClipboardData           = user32.MustFindProc("SetClipboardData")
	getDC                      = user32.MustFindProc("GetDC")

	kernel32     = syscall.NewLazyDLL("kernel32")
	globalAlloc  = kernel32.NewProc("GlobalAlloc")
	globalFree   = kernel32.NewProc("GlobalFree")
	globalLock   = kernel32.NewProc("GlobalLock")
	globalUnlock = kernel32.NewProc("GlobalUnlock")
	lstrcpy      = kernel32.NewProc("lstrcpyW")

	libgdi32           = syscall.NewLazyDLL("gdi32.dll")
	createCompatibleDC = libgdi32.NewProc("CreateCompatibleDC")
	getObject          = libgdi32.NewProc("GetObjectW")
	selectObject       = libgdi32.NewProc("SelectObject")
	getDIBits          = libgdi32.NewProc("GetDIBits")
)

// waitOpenClipboard opens the clipboard, waiting for up to a second to do so.
func waitOpenClipboard() error {
	started := time.Now()
	limit := started.Add(time.Second)
	var r uintptr
	var err error
	for time.Now().Before(limit) {
		r, _, err = openClipboard.Call(0)
		if r != 0 {
			return nil
		}
		time.Sleep(time.Millisecond)
	}
	return err
}

type BITMAPINFOHEADER struct {
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

func bmp2png(w *bytes.Buffer) ([]byte, error) {
	var err error
	var src image.Image
	src, err = bmp.Decode(w)
	if err != nil {
		return nil, err
	}
	out := bytes.NewBuffer([]byte{})
	png.Encode(out, src)
	return out.Bytes(), nil
}

func PasteImg(outputPng bool) ([]byte, error) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if formatAvailable, _, err := isClipboardFormatAvailable.Call(cfBITMAP); formatAvailable == 0 {
		return nil, err
	}
	err := waitOpenClipboard()
	if err != nil {
		return nil, err
	}

	h, _, err := getClipboardData.Call(cfBITMAP)
	if h == 0 {
		_, _, _ = closeClipboard.Call()
		return nil, err
	}

	hdc, _, err := getDC.Call(0)
	hdcmem, _, err := createCompatibleDC.Call(hdc)
	selectObject.Call(hdcmem, h)
	bm := make([]byte, 28)
	var size uint = 28
	getObject.Call(h, uintptr(unsafe.Pointer(&size)), uintptr(unsafe.Pointer(&bm[0])))

	width := binary.LittleEndian.Uint32(bm[4:8])
	height := binary.LittleEndian.Uint32(bm[8:12])
	bmBitsPixel := bm[18]

	a := new(BITMAPINFOHEADER)
	a.BiSize = 40
	a.BiWidth = int32(width)
	a.BiHeight = int32(height)
	a.BiPlanes = 1
	a.BiBitCount = uint16(bmBitsPixel)
	a.BiSizeImage = (width*uint32(bmBitsPixel) + 31) / 32 * 4 * height
	b := make([]byte, a.BiSizeImage)

	getDIBits.Call(hdc, h, 0, uintptr(height), uintptr(unsafe.Pointer(&b[0])), uintptr(unsafe.Pointer(a)), 0)
	w := bytes.NewBuffer([]byte{})

	w.Write([]byte{0x42, 0x4d})
	bytesBuffer := bytes.NewBuffer([]byte{})
	binary.Write(bytesBuffer, binary.LittleEndian, a.BiSizeImage+54)
	w.Write(bytesBuffer.Bytes())
	temp := make([]byte, 8)
	temp[4] = 0x36
	w.Write(temp)
	w.Write([]byte{0x28, 0x00, 0x00, 0x00})
	bytesBuffer.Reset()
	binary.Write(bytesBuffer, binary.LittleEndian, a.BiWidth)
	binary.Write(bytesBuffer, binary.LittleEndian, a.BiHeight)
	binary.Write(bytesBuffer, binary.LittleEndian, a.BiPlanes)
	binary.Write(bytesBuffer, binary.LittleEndian, a.BiBitCount)
	var temp1 uint32
	temp1 = 0
	binary.Write(bytesBuffer, binary.LittleEndian, temp1)
	binary.Write(bytesBuffer, binary.LittleEndian, temp1)
	w.Write(bytesBuffer.Bytes())
	bytesBuffer.Reset()
	temp = make([]byte, 16)
	w.Write(temp)
	w.Write(b)

	var output []byte
	if outputPng == true {
		output, err = bmp2png(w)
	} else {
		output = w.Bytes()
		err = nil
	}
	closeClipboard.Call()
	return output, err
}

func PasteTXT() (string, error) {
	// LockOSThread ensure that the whole method will keep executing on the same thread from begin to end (it actually locks the goroutine thread attribution).
	// Otherwise if the goroutine switch thread during execution (which is a common practice), the OpenClipboard and CloseClipboard will happen on two different threads, and it will result in a clipboard deadlock.
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if formatAvailable, _, err := isClipboardFormatAvailable.Call(cfUnicodetext); formatAvailable == 0 {
		return "", err
	}
	err := waitOpenClipboard()
	if err != nil {
		return "", err
	}

	h, _, err := getClipboardData.Call(cfUnicodetext)
	if h == 0 {
		_, _, _ = closeClipboard.Call()
		return "", err
	}

	l, _, err := globalLock.Call(h)
	if l == 0 {
		_, _, _ = closeClipboard.Call()
		return "", err
	}

	text := syscall.UTF16ToString((*[1 << 20]uint16)(unsafe.Pointer(l))[:])

	r, _, err := globalUnlock.Call(h)
	if r == 0 {
		_, _, _ = closeClipboard.Call()
		return "", err
	}

	closed, _, err := closeClipboard.Call()
	if closed == 0 {
		return "", err
	}
	return text, nil
}
