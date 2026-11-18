package platform

import (
	"fmt"
	"log"
	"syscall"
	"unsafe"
)

const (
	cursorHintSize = 24
	cursorHintGap  = 16

	wsExTopmost     = 0x00000008
	wsExTransparent = 0x00000020
	wsExToolWindow  = 0x00000080
	wsExLayered     = 0x00080000
	wsExNoActivate  = 0x08000000
	wsDisabled      = 0x08000000
	wsPopup         = 0x80000000

	swShowNoActivate  = 4
	swpHideNoActivate = 0x0080 | 0x0010 | 0x0004 | 0x0002 | 0x0001 // HIDEWINDOW | NOACTIVATE | NOZORDER | NOMOVE | NOSIZE
)

var (
	showWindow          = user32.NewProc("ShowWindow")
	setWindowPos        = user32.NewProc("SetWindowPos")
	unregisterClassW    = user32.NewProc("UnregisterClassW")
	monitorFromRect     = user32.NewProc("MonitorFromRect")
	getMonitorInfoW     = user32.NewProc("GetMonitorInfoW")
	updateLayeredWindow = user32.NewProc("UpdateLayeredWindow")
	drawIconEx          = user32.NewProc("DrawIconEx")
	destroyIcon         = user32.NewProc("DestroyIcon")

	gdi32              = syscall.NewLazyDLL("gdi32.dll")
	createCompatibleDC = gdi32.NewProc("CreateCompatibleDC")
	createDIBSection   = gdi32.NewProc("CreateDIBSection")
	selectObject       = gdi32.NewProc("SelectObject")
	deleteDC           = gdi32.NewProc("DeleteDC")
	deleteObject       = gdi32.NewProc("DeleteObject")
)

type winRect struct{ Left, Top, Right, Bottom int32 }

type monitorInfo struct {
	Size          uint32
	Monitor, Work winRect
	Flags         uint32
}

type bitmapInfoHeader struct {
	Size                         uint32
	Width, Height                int32
	Planes, BitCount             uint16
	Compression, SizeImage       uint32
	XPelsPerMeter, YPelsPerMeter int32
	ClrUsed, ClrImportant        uint32
}

type winCursorHint struct {
	hwnd, dc, bitmap uintptr
	instance         uintptr
	class            *uint16
	visible          bool
}

// NewCursorHint creates a hidden hint on the UI thread when enabled.
func NewCursorHint(enabled bool) CursorHint {
	if !enabled {
		return noopCursorHint{}
	}
	h := &winCursorHint{}
	if err := h.init(); err != nil {
		h.Close()
		log.Printf("cursor hint unavailable: %v", err)
		return noopCursorHint{}
	}
	return h
}

func (h *winCursorHint) init() error {
	h.instance, _, _ = getModuleHandleW.Call(0)
	if err := h.drawBusyIcon(); err != nil {
		return err
	}
	name := u16("ClipboardTranslatorCursorHint")
	wc := wndClassEx{HInstance: h.instance, LpszClassName: name, LpfnWndProc: defWindowProcW.Addr()}
	wc.CbSize = uint32(unsafe.Sizeof(wc))
	if atom, _, err := registerClassExW.Call(uintptr(unsafe.Pointer(&wc))); atom == 0 {
		return fmt.Errorf("RegisterClassExW: %w", err)
	}
	h.class = name
	// Layered + transparent passes pointer events through even over opaque pixels.
	const exStyle = wsExLayered | wsExTransparent | wsExTopmost | wsExNoActivate | wsExToolWindow
	hwnd, _, err := createWindowExW.Call(exStyle, uintptr(unsafe.Pointer(name)), 0,
		wsPopup|wsDisabled, 0, 0, cursorHintSize, cursorHintSize, 0, 0, h.instance, 0)
	if hwnd == 0 {
		return fmt.Errorf("CreateWindowExW: %w", err)
	}
	h.hwnd = hwnd
	return nil
}

func (h *winCursorHint) drawBusyIcon() error {
	icon, _, err := loadImageW.Call(h.instance, iconBusyID, imageIcon, cursorHintSize, cursorHintSize, 0)
	if icon == 0 {
		return fmt.Errorf("load busy icon: %w", err)
	}
	defer destroyIcon.Call(icon)

	dc, _, err := createCompatibleDC.Call(0)
	if dc == 0 {
		return fmt.Errorf("CreateCompatibleDC: %w", err)
	}
	h.dc = dc
	header := bitmapInfoHeader{Width: cursorHintSize, Height: -cursorHintSize, Planes: 1, BitCount: 32}
	header.Size = uint32(unsafe.Sizeof(header))
	var pixels *byte
	bitmap, _, err := createDIBSection.Call(dc, uintptr(unsafe.Pointer(&header)), 0, uintptr(unsafe.Pointer(&pixels)), 0, 0)
	if bitmap == 0 {
		return fmt.Errorf("CreateDIBSection: %w", err)
	}
	h.bitmap = bitmap
	clear(unsafe.Slice(pixels, cursorHintSize*cursorHintSize*4))
	if old, _, err := selectObject.Call(dc, bitmap); old == 0 {
		return fmt.Errorf("SelectObject: %w", err)
	}
	// Draw the alpha-bearing icon over transparent black for UpdateLayeredWindow.
	const diNormal = 3
	if ok, _, err := drawIconEx.Call(dc, 0, 0, icon, cursorHintSize, cursorHintSize, 0, 0, diNormal); ok == 0 {
		return fmt.Errorf("DrawIconEx: %w", err)
	}
	return nil
}

func (h *winCursorHint) Show() {
	if h.hwnd == 0 {
		return
	}
	var cursor winPoint
	if ok, _, _ := getCursorPos.Call(uintptr(unsafe.Pointer(&cursor))); ok == 0 {
		h.Hide()
		return
	}
	// Use a one-pixel rectangle to choose the monitor containing the pointer.
	point := winRect{cursor.X, cursor.Y, cursor.X + 1, cursor.Y + 1}
	const monitorDefaultToNearest = 2
	monitor, _, _ := monitorFromRect.Call(uintptr(unsafe.Pointer(&point)), monitorDefaultToNearest)
	var info monitorInfo
	info.Size = uint32(unsafe.Sizeof(info))
	if ok, _, _ := getMonitorInfoW.Call(monitor, uintptr(unsafe.Pointer(&info))); ok == 0 {
		h.Hide()
		return
	}
	pos := winPoint{cursorHintCoordinate(cursor.X, info.Work.Left, info.Work.Right),
		cursorHintCoordinate(cursor.Y, info.Work.Top, info.Work.Bottom)}
	size, source := winPoint{cursorHintSize, cursorHintSize}, winPoint{}
	// BLENDFUNCTION: AC_SRC_OVER, reserved, full opacity, AC_SRC_ALPHA.
	blend := [4]byte{0, 0, 255, 1}
	const ulwAlpha = 2
	if ok, _, err := updateLayeredWindow.Call(h.hwnd, 0, uintptr(unsafe.Pointer(&pos)), uintptr(unsafe.Pointer(&size)), h.dc,
		uintptr(unsafe.Pointer(&source)), 0, uintptr(unsafe.Pointer(&blend)), ulwAlpha); ok == 0 {
		h.Hide()
		log.Printf("could not show cursor hint: %v", err)
		return
	}
	if !h.visible {
		showWindow.Call(h.hwnd, swShowNoActivate)
		h.visible = true
	}
}

func cursorHintCoordinate(cursor, start, end int32) int32 {
	pos := cursor + cursorHintGap
	if pos+cursorHintSize > end {
		pos = cursor - cursorHintGap - cursorHintSize
	}
	return max(start, min(pos, end-cursorHintSize))
}

func (h *winCursorHint) Hide() {
	if h.visible {
		setWindowPos.Call(h.hwnd, 0, 0, 0, 0, 0, swpHideNoActivate)
		h.visible = false
	}
}

func (h *winCursorHint) Close() {
	if h.hwnd != 0 {
		destroyWindow.Call(h.hwnd)
	}
	if h.class != nil {
		unregisterClassW.Call(uintptr(unsafe.Pointer(h.class)), h.instance)
	}
	if h.dc != 0 {
		deleteDC.Call(h.dc)
	}
	if h.bitmap != 0 {
		deleteObject.Call(h.bitmap)
	}
	*h = winCursorHint{}
}
