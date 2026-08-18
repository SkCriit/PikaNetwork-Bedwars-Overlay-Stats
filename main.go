//go:build windows

package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"image/png"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
	"unsafe"
)

// PikaStats Overlay - standalone Lunar Client log overlay for Windows.
// No injection, no Minecraft mod loader, and no account credentials.

const (
	appName        = "PikaStats Overlay"
	appVersion     = "3.6.2"
	windowClass    = "PikaStatsOverlayWindowClass"
	baseWidth      = int32(650)
	headerHeight   = int32(58)
	columnsHeight  = int32(30)
	rowHeight      = int32(36)
	footerHeight   = int32(28)
	minBodyHeight  = int32(64)
	maxPlayers     = 32
	WM_APP_REFRESH = 0x8001
	WM_APP_TOGGLE  = 0x8002
)

const (
	WM_DESTROY        = 0x0002
	WM_PAINT          = 0x000F
	WM_CLOSE          = 0x0010
	WM_MOVE           = 0x0003
	WM_LBUTTONDOWN    = 0x0201
	WM_LBUTTONUP      = 0x0202
	WM_MOUSEMOVE      = 0x0200
	WM_NCHITTEST      = 0x0084
	WM_ERASEBKGND     = 0x0014
	WM_EXITSIZEMOVE   = 0x0232
	HTCLIENT          = 1
	HTCAPTION         = 2
	WS_POPUP          = 0x80000000
	WS_EX_TOPMOST     = 0x00000008
	WS_EX_TOOLWINDOW  = 0x00000080
	WS_EX_LAYERED     = 0x00080000
	SW_HIDE           = 0
	SW_SHOWNOACTIVATE = 4
	SWP_NOMOVE        = 0x0002
	SWP_NOZORDER      = 0x0004
	SWP_NOACTIVATE    = 0x0010
	HWND_TOPMOST      = ^uintptr(0)
	LWA_ALPHA         = 0x00000002
	VK_X              = 0x58
	VK_TAB            = 0x09
	VK_T              = 0x54
	VK_RETURN         = 0x0D
	INPUT_KEYBOARD    = 1
	KEYEVENTF_KEYUP   = 0x0002
	KEYEVENTF_UNICODE = 0x0004
	DIB_RGB_COLORS    = 0
	SRCCOPY           = 0x00CC0020
	BI_RGB            = 0
	IDC_ARROW         = 32512
	COLOR_WINDOW      = 5
	PS_SOLID          = 0
	TRANSPARENT       = 1
	DT_LEFT           = 0x00000000
	DT_CENTER         = 0x00000001
	DT_RIGHT          = 0x00000002
	DT_VCENTER        = 0x00000004
	DT_SINGLELINE     = 0x00000020
	FW_NORMAL         = 400
	FW_SEMIBOLD       = 600
	FW_BOLD           = 700
)

var (
	user32   = syscall.NewLazyDLL("user32.dll")
	gdi32    = syscall.NewLazyDLL("gdi32.dll")
	kernel32 = syscall.NewLazyDLL("kernel32.dll")

	procRegisterClassExW           = user32.NewProc("RegisterClassExW")
	procCreateWindowExW            = user32.NewProc("CreateWindowExW")
	procDefWindowProcW             = user32.NewProc("DefWindowProcW")
	procShowWindow                 = user32.NewProc("ShowWindow")
	procUpdateWindow               = user32.NewProc("UpdateWindow")
	procGetMessageW                = user32.NewProc("GetMessageW")
	procTranslateMessage           = user32.NewProc("TranslateMessage")
	procDispatchMessageW           = user32.NewProc("DispatchMessageW")
	procPostQuitMessage            = user32.NewProc("PostQuitMessage")
	procBeginPaint                 = user32.NewProc("BeginPaint")
	procEndPaint                   = user32.NewProc("EndPaint")
	procInvalidateRect             = user32.NewProc("InvalidateRect")
	procSetWindowPos               = user32.NewProc("SetWindowPos")
	procGetSystemMetrics           = user32.NewProc("GetSystemMetrics")
	procSetLayeredWindowAttributes = user32.NewProc("SetLayeredWindowAttributes")
	procPostMessageW               = user32.NewProc("PostMessageW")
	procSendMessageW               = user32.NewProc("SendMessageW")
	procReleaseCapture             = user32.NewProc("ReleaseCapture")
	procGetWindowRect              = user32.NewProc("GetWindowRect")
	procSetWindowRgn               = user32.NewProc("SetWindowRgn")
	procLoadCursorW                = user32.NewProc("LoadCursorW")
	procDestroyWindow              = user32.NewProc("DestroyWindow")
	procGetAsyncKeyState           = user32.NewProc("GetAsyncKeyState")
	procSendInput                  = user32.NewProc("SendInput")
	procGetForegroundWindow        = user32.NewProc("GetForegroundWindow")
	procGetWindowTextW             = user32.NewProc("GetWindowTextW")

	procCreateSolidBrush   = gdi32.NewProc("CreateSolidBrush")
	procCreatePen          = gdi32.NewProc("CreatePen")
	procSelectObject       = gdi32.NewProc("SelectObject")
	procDeleteObject       = gdi32.NewProc("DeleteObject")
	procRoundRect          = gdi32.NewProc("RoundRect")
	procRectangle          = gdi32.NewProc("Rectangle")
	procMoveToEx           = gdi32.NewProc("MoveToEx")
	procLineTo             = gdi32.NewProc("LineTo")
	procSetBkMode          = gdi32.NewProc("SetBkMode")
	procSetTextColor       = gdi32.NewProc("SetTextColor")
	procCreateFontW        = gdi32.NewProc("CreateFontW")
	procCreateRoundRectRgn = gdi32.NewProc("CreateRoundRectRgn")
	procTextOutW           = gdi32.NewProc("TextOutW")
	procStretchDIBits      = gdi32.NewProc("StretchDIBits")

	procGetModuleHandleW = kernel32.NewProc("GetModuleHandleW")
)

type POINT struct{ X, Y int32 }
type RECT struct{ Left, Top, Right, Bottom int32 }
type MSG struct {
	Hwnd           uintptr
	Message        uint32
	WParam, LParam uintptr
	Time           uint32
	Pt             POINT
	LPrivate       uint32
}
type PAINTSTRUCT struct {
	Hdc         uintptr
	FErase      int32
	RcPaint     RECT
	FRestore    int32
	FIncUpdate  int32
	RGBReserved [32]byte
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
type RGBQUAD struct {
	Blue, Green, Red, Reserved byte
}
type BITMAPINFO struct {
	BmiHeader BITMAPINFOHEADER
	BmiColors [1]RGBQUAD
}
type Avatar struct {
	Width, Height int32
	Pixels        []byte // BGRA, top-down
}
type WNDCLASSEX struct {
	CbSize        uint32
	Style         uint32
	LpfnWndProc   uintptr
	CbClsExtra    int32
	CbWndExtra    int32
	HInstance     uintptr
	HIcon         uintptr
	HCursor       uintptr
	HbrBackground uintptr
	LpszMenuName  *uint16
	LpszClassName *uint16
	HIconSm       uintptr
}

// INPUT uses a 32-byte union on 64-bit Windows. Keeping the union as raw bytes
// makes sizeof(INPUT) match the Win32 ABI (40 bytes on x64).
type INPUT struct {
	Type uint32
	_    uint32
	Data [32]byte
}

type KEYBDINPUT struct {
	WVk         uint16
	WScan       uint16
	DwFlags     uint32
	Time        uint32
	DwExtraInfo uintptr
}

type PlayerStats struct {
	Username    string
	Level       int64
	FinalKills  int64
	FinalDeaths int64
	Beds        int64
	Wins        int64
	GamesPlayed int64
	Losses      int64
	FKDR        float64
	Infinite    bool
	State       string // loading, ok, unavailable, api
	Updated     time.Time
}

type AppState struct {
	sync.RWMutex
	HWND            uintptr
	Pika            bool
	BedWars         bool
	GameLive        bool
	Visible         bool
	ManualHidden    bool
	LogPath         string
	Status          string
	Players         map[string]*PlayerStats
	Order           []string
	X, Y            int32
	ScanUntil       time.Time
	ExpectedPlayers int
	SortBy          string
}

type logCursor struct {
	Offset  int64
	Partial string
}

var state = AppState{Players: make(map[string]*PlayerStats), Status: "Waiting for Lunar Client...", X: -1, Y: 72, SortBy: "auto"}
var refreshPending int32
var requestQueue = make(chan string, 64)
var queuedMu sync.Mutex
var queued = map[string]bool{}
var cacheMu sync.Mutex
var cache = map[string]*PlayerStats{}
var httpClient = &http.Client{
	Timeout: 5 * time.Second,
	Transport: &http.Transport{
		MaxIdleConns:        16,
		MaxIdleConnsPerHost: 8,
		IdleConnTimeout:     30 * time.Second,
	},
}
var apiRateTicker = time.NewTicker(100 * time.Millisecond)
var lastWindowHeight int32
var debugMu sync.Mutex
var debugPath string

var avatarMu sync.RWMutex
var avatars = map[string]*Avatar{}
var avatarQueuedMu sync.Mutex
var avatarQueued = map[string]bool{}
var avatarQueue = make(chan string, 64)

var rosterCandidateMu sync.Mutex
var rosterCandidate []string
var rosterCandidateAt time.Time
var lastTabAt time.Time

var identityMu sync.RWMutex
var localUsername string
var chatCommandMu sync.Mutex
var lastChatCommand string
var lastChatCommandAt time.Time
var chatSendMu sync.Mutex

var usernameRE = regexp.MustCompile(`\b[A-Za-z0-9_]{3,16}\b`)
var mcColorRE = regexp.MustCompile(`(?:§|\\u00a7)[0-9A-FK-ORa-fk-or]`)
var settingUserRE = regexp.MustCompile(`(?i)Setting user:\s*([A-Za-z0-9_]{3,16})`)
var statsCommandRE = regexp.MustCompile(`(?i)(?:^|\s)-stats\s+([A-Za-z0-9_]{3,16})\s*$`)

func rgb(r, g, b byte) uintptr { return uintptr(r) | uintptr(g)<<8 | uintptr(b)<<16 }
func utf16p(s string) *uint16  { p, _ := syscall.UTF16PtrFromString(s); return p }

func main() {
	initDebug()
	for i := 0; i < 4; i++ {
		go apiWorker()
	}
	// Heads are best-effort and use their own tiny queue so skin lookups can
	// never hold up stats requests or the overlay UI.
	for i := 0; i < 1; i++ {
		go avatarWorker()
	}
	go watchLunarLog()
	go watchTabKey()
	go watchToggleKey()
	runWindow()
}

func initDebug() {
	base := os.Getenv("LOCALAPPDATA")
	if base == "" {
		base, _ = os.UserHomeDir()
	}
	dir := filepath.Join(base, "PikaStatsOverlay")
	_ = os.MkdirAll(dir, 0755)
	debugPath = filepath.Join(dir, "debug.log")
	_ = os.WriteFile(debugPath, []byte("PikaStats Overlay "+appVersion+" debug log\r\n"), 0644)
}

func debugf(format string, args ...any) {
	if debugPath == "" {
		return
	}
	debugMu.Lock()
	defer debugMu.Unlock()
	f, err := os.OpenFile(debugPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = fmt.Fprintf(f, "%s  %s\r\n", time.Now().Format("15:04:05.000"), fmt.Sprintf(format, args...))
}

func runWindow() {
	hinst, _, _ := procGetModuleHandleW.Call(0)
	cursor, _, _ := procLoadCursorW.Call(0, IDC_ARROW)
	className := utf16p(windowClass)
	wc := WNDCLASSEX{CbSize: uint32(unsafe.Sizeof(WNDCLASSEX{})), LpfnWndProc: syscall.NewCallback(wndProc), HInstance: hinst, HCursor: cursor, HbrBackground: 0, LpszClassName: className}
	if r, _, _ := procRegisterClassExW.Call(uintptr(unsafe.Pointer(&wc))); r == 0 {
		return
	}

	screenW, _, _ := procGetSystemMetrics.Call(0)
	x := int32(screenW) - baseWidth - 24
	state.Lock()
	if state.X >= 0 {
		x = state.X
	}
	y := state.Y
	state.Unlock()
	h := desiredHeight()
	hwnd, _, _ := procCreateWindowExW.Call(WS_EX_TOPMOST|WS_EX_TOOLWINDOW|WS_EX_LAYERED, uintptr(unsafe.Pointer(className)), uintptr(unsafe.Pointer(utf16p(appName))), WS_POPUP, uintptr(x), uintptr(y), uintptr(baseWidth), uintptr(h), 0, 0, hinst, 0)
	if hwnd == 0 {
		return
	}
	state.Lock()
	state.HWND = hwnd
	state.Unlock()
	procSetLayeredWindowAttributes.Call(hwnd, 0, 242, LWA_ALPHA)
	applyRoundedRegion(hwnd, baseWidth, h)
	procSetWindowPos.Call(hwnd, HWND_TOPMOST, 0, 0, 0, 0, SWP_NOMOVE|SWP_NOMOVE|SWP_NOACTIVATE|0x0001) // NOSIZE=1
	// Always show immediately so startup can never look like a failed launch.
	state.Lock()
	state.Visible = true
	state.ManualHidden = false
	state.Unlock()
	procShowWindow.Call(hwnd, SW_SHOWNOACTIVATE)
	procUpdateWindow.Call(hwnd)

	var msg MSG
	for {
		r, _, _ := procGetMessageW.Call(uintptr(unsafe.Pointer(&msg)), 0, 0, 0)
		if int32(r) <= 0 {
			break
		}
		procTranslateMessage.Call(uintptr(unsafe.Pointer(&msg)))
		procDispatchMessageW.Call(uintptr(unsafe.Pointer(&msg)))
	}
}

func wndProc(hwnd uintptr, msg uint32, wParam, lParam uintptr) uintptr {
	switch msg {
	case WM_ERASEBKGND:
		return 1
	case WM_PAINT:
		paint(hwnd)
		return 0
	case WM_APP_TOGGLE:
		toggleVisible(hwnd)
		return 0
	case WM_LBUTTONDOWN:
		x := int32(int16(lParam & 0xFFFF))
		y := int32(int16((lParam >> 16) & 0xFFFF))
		if y < headerHeight && x > baseWidth-48 {
			procDestroyWindow.Call(hwnd)
			return 0
		}
		if y < headerHeight {
			procReleaseCapture.Call()
			procSendMessageW.Call(hwnd, 0x00A1 /*WM_NCLBUTTONDOWN*/, HTCAPTION, 0)
			return 0
		}
	case WM_EXITSIZEMOVE:
		saveWindowPosition(hwnd)
		return 0
	case WM_APP_REFRESH:
		atomic.StoreInt32(&refreshPending, 0)
		resizeAndRepaint(hwnd)
		return 0
	case WM_CLOSE:
		procDestroyWindow.Call(hwnd)
		return 0
	case WM_DESTROY:
		saveWindowPosition(hwnd)
		procPostQuitMessage.Call(0)
		return 0
	}
	r, _, _ := procDefWindowProcW.Call(hwnd, uintptr(msg), wParam, lParam)
	return r
}

func desiredHeight() int32 {
	state.RLock()
	n := len(state.Order)
	state.RUnlock()
	body := int32(n) * rowHeight
	if body < minBodyHeight {
		body = minBodyHeight
	}
	return headerHeight + columnsHeight + body + footerHeight
}

func resizeAndRepaint(hwnd uintptr) {
	h := desiredHeight()
	if h != lastWindowHeight {
		procSetWindowPos.Call(hwnd, 0, 0, 0, uintptr(baseWidth), uintptr(h), SWP_NOMOVE|SWP_NOZORDER|SWP_NOACTIVATE)
		applyRoundedRegion(hwnd, baseWidth, h)
		lastWindowHeight = h
	}
	procInvalidateRect.Call(hwnd, 0, 1)
}

func applyRoundedRegion(hwnd uintptr, w, h int32) {
	rgn, _, _ := procCreateRoundRectRgn.Call(0, 0, uintptr(w+1), uintptr(h+1), 18, 18)
	procSetWindowRgn.Call(hwnd, rgn, 1)
}

func toggleVisible(hwnd uintptr) {
	state.Lock()
	state.Visible = !state.Visible
	state.ManualHidden = !state.Visible
	vis := state.Visible
	state.Unlock()
	if vis {
		procShowWindow.Call(hwnd, SW_SHOWNOACTIVATE)
		procSetWindowPos.Call(hwnd, HWND_TOPMOST, 0, 0, 0, 0, 0x0001|SWP_NOMOVE|SWP_NOACTIVATE)
		procInvalidateRect.Call(hwnd, 0, 1)
	} else {
		procShowWindow.Call(hwnd, SW_HIDE)
	}
}

func autoShow() {
	state.Lock()
	hwnd := state.HWND
	if !state.ManualHidden {
		state.Visible = true
	}
	vis := state.Visible
	state.Unlock()
	if hwnd != 0 && vis {
		procShowWindow.Call(hwnd, SW_SHOWNOACTIVATE)
		procSetWindowPos.Call(hwnd, HWND_TOPMOST, 0, 0, 0, 0, 0x0001|SWP_NOMOVE|SWP_NOACTIVATE)
		notifyRefresh()
	}
}

func notifyRefresh() {
	state.RLock()
	hwnd := state.HWND
	state.RUnlock()
	if hwnd == 0 {
		return
	}
	// Coalesce bursts of log/API/avatar updates into one pending UI refresh.
	// This prevents the window message queue from being flooded during a TAB scan.
	if !atomic.CompareAndSwapInt32(&refreshPending, 0, 1) {
		return
	}
	r, _, _ := procPostMessageW.Call(hwnd, WM_APP_REFRESH, 0, 0)
	if r == 0 {
		atomic.StoreInt32(&refreshPending, 0)
	}
}

func paint(hwnd uintptr) {
	defer func() {
		if r := recover(); r != nil {
			debugf("paint recovered panic: %v", r)
		}
	}()
	var ps PAINTSTRUCT
	hdc, _, _ := procBeginPaint.Call(hwnd, uintptr(unsafe.Pointer(&ps)))
	if hdc == 0 {
		return
	}
	defer procEndPaint.Call(hwnd, uintptr(unsafe.Pointer(&ps)))
	procSetBkMode.Call(hdc, TRANSPARENT)

	state.RLock()
	pika := state.Pika
	bw := state.BedWars
	live := state.GameLive
	status := state.Status
	logPath := state.LogPath
	rows := make([]*PlayerStats, 0, len(state.Order))
	for _, k := range state.Order {
		if p := state.Players[k]; p != nil {
			cp := *p
			rows = append(rows, &cp)
		}
	}
	state.RUnlock()

	sort.SliceStable(rows, func(i, j int) bool {
		a, b := rows[i], rows[j]
		if a.State == "ok" && b.State != "ok" {
			return true
		}
		if a.State != "ok" && b.State == "ok" {
			return false
		}
		if a.State != "ok" && b.State != "ok" {
			return strings.ToLower(a.Username) < strings.ToLower(b.Username)
		}

		// Automatic strongest-to-weakest ranking. It considers every visible
		// stat instead of making the user click a sort column each queue.
		ad, bd := isDangerous(a), isDangerous(b)
		if ad != bd {
			return ad
		}
		aa, ba := isLikelyAlt(a), isLikelyAlt(b)
		if aa != ba {
			return aa
		}
		as, bs := threatScore(a), threatScore(b)
		if as != bs {
			return as > bs
		}
		if a.Infinite != b.Infinite {
			return a.Infinite
		}
		if a.FKDR != b.FKDR {
			return a.FKDR > b.FKDR
		}
		if a.FinalKills != b.FinalKills {
			return a.FinalKills > b.FinalKills
		}
		if a.Wins != b.Wins {
			return a.Wins > b.Wins
		}
		if a.Beds != b.Beds {
			return a.Beds > b.Beds
		}
		if a.Level != b.Level {
			return a.Level > b.Level
		}
		return strings.ToLower(a.Username) < strings.ToLower(b.Username)
	})

	h := desiredHeight()
	drawRoundBox(hdc, 0, 0, baseWidth, h, 14, rgb(17, 20, 27), rgb(55, 63, 78))
	drawRoundBox(hdc, 1, 1, baseWidth-1, headerHeight, 14, rgb(23, 27, 36), rgb(23, 27, 36))

	titleFont := makeFont(20, FW_BOLD)
	bodyFont := makeFont(15, FW_NORMAL)
	smallFont := makeFont(13, FW_NORMAL)
	semiFont := makeFont(14, FW_SEMIBOLD)
	defer del(titleFont)
	defer del(bodyFont)
	defer del(smallFont)
	defer del(semiFont)

	text(hdc, titleFont, 20, 12, "PikaStats Overlay", rgb(239, 242, 248))
	text(hdc, smallFont, 21, 36, "X show / hide   •   AUTO strongest → weakest   •   -stats Player", rgb(139, 149, 166))
	text(hdc, semiFont, baseWidth-32, 18, "×", rgb(184, 192, 205))

	chip := "WAITING"
	chipColor := rgb(118, 126, 142)
	if pika {
		chip = "PIKA"
		chipColor = rgb(84, 180, 145)
	}
	if bw {
		chip = "BEDWARS"
		chipColor = rgb(89, 150, 230)
	}
	if live {
		chip = "LIVE"
		chipColor = rgb(196, 109, 116)
	}
	chipW := int32(len(chip)*8 + 22)
	drawRoundBox(hdc, baseWidth-chipW-58, 17, baseWidth-58, 41, 12, blend(chipColor, rgb(17, 20, 27), 35), chipColor)
	text(hdc, smallFont, baseWidth-chipW-47, 22, chip, chipColor)

	y := headerHeight
	fillRect(hdc, 0, y, baseWidth, y+columnsHeight, rgb(20, 23, 31))
	text(hdc, smallFont, 20, y+8, "PLAYER", rgb(141, 151, 168))
	text(hdc, smallFont, 285, y+8, "LVL", rgb(141, 151, 168))
	text(hdc, smallFont, 346, y+8, "FKDR", rgb(141, 151, 168))
	text(hdc, smallFont, 419, y+8, "FINALS", rgb(141, 151, 168))
	text(hdc, smallFont, 505, y+8, "BEDS", rgb(141, 151, 168))
	text(hdc, smallFont, 574, y+8, "WINS", rgb(141, 151, 168))

	y += columnsHeight
	if len(rows) == 0 {
		msg := "Waiting for BedWars players..."
		sub := "Open chat with T or /, type one space, then press TAB. Player names are read from Lunar's active log."
		if !pika {
			msg = "Waiting for PikaNetwork..."
			sub = "Join PikaNetwork, then use T → Space → TAB in the BedWars queue."
		}
		text(hdc, bodyFont, 20, y+18, msg, rgb(224, 229, 238))
		text(hdc, smallFont, 20, y+43, sub, rgb(132, 142, 158))
	} else {
		for i, p := range rows {
			ry := y + int32(i)*rowHeight
			if isDangerous(p) {
				fillRect(hdc, 8, ry, baseWidth-8, ry+rowHeight, rgb(45, 24, 31))
			} else if isLikelyAlt(p) {
				fillRect(hdc, 8, ry, baseWidth-8, ry+rowHeight, rgb(42, 33, 20))
			} else if i%2 == 1 {
				fillRect(hdc, 8, ry, baseWidth-8, ry+rowHeight, rgb(19, 22, 29))
			}
			name := p.Username
			nameColor := rgb(231, 235, 242)
			if isDangerous(p) {
				name += "  [!]"
				nameColor = statRed
			}
			if isLikelyAlt(p) {
				name += "  [ALT]"
				if !isDangerous(p) {
					nameColor = statOrange
				}
			}
			// Best-effort Minecraft head. If a username cannot be resolved by the
			// avatar service (common for cracked/offline accounts), the row simply
			// renders without a head and everything else still works.
			if av := getAvatar(p.Username); av != nil {
				drawAvatar(hdc, 18, ry+5, 26, 26, av)
			}
			text(hdc, semiFont, 52, ry+11, name, nameColor)
			if p.State == "loading" {
				text(hdc, smallFont, 285, ry+12, "Loading...", rgb(126, 143, 170))
				continue
			}
			if p.State == "unavailable" {
				text(hdc, smallFont, 285, ry+12, "Stats unavailable", rgb(178, 132, 136))
				continue
			}
			if p.State == "api" {
				text(hdc, smallFont, 285, ry+12, "API unavailable", rgb(178, 132, 136))
				continue
			}
			text(hdc, bodyFont, 285, ry+10, fmt.Sprintf("%d", p.Level), levelColor(p.Level))
			fk := "0.00"
			if p.Infinite {
				fk = "∞"
			} else {
				fk = fmt.Sprintf("%.2f", p.FKDR)
			}
			text(hdc, bodyFont, 346, ry+10, fk, fkdrColor(p.FKDR, p.Infinite))
			text(hdc, bodyFont, 419, ry+10, comma(p.FinalKills), finalsColor(p.FinalKills))
			text(hdc, bodyFont, 505, ry+10, comma(p.Beds), bedsColor(p.Beds))
			text(hdc, bodyFont, 574, ry+10, comma(p.Wins), winsColor(p.Wins))
		}
	}

	fy := h - footerHeight
	line(hdc, 12, fy, baseWidth-12, fy, rgb(46, 53, 66))
	foot := status
	if len(foot) > 76 {
		foot = foot[:73] + "..."
	}
	if pika && len(rows) > 0 {
		foot = fmt.Sprintf("%d player%s • AUTO ranked strongest → weakest • [!] threat • [ALT] suspicious stat mismatch • X toggles", len(rows), plural(len(rows)))
	}
	if logPath != "" && !pika {
		foot = "Lunar log found • waiting for PikaNetwork"
	}
	text(hdc, smallFont, 18, fy+8, foot, rgb(117, 128, 145))
}

// PvP-style stat tiers requested by the user.
// Gray = low/normal, green = good, orange = strong, red = very strong,
// purple = exceptional.
var (
	statGray   = rgb(145, 153, 166)
	statGreen  = rgb(91, 201, 123)
	statOrange = rgb(239, 166, 73)
	statRed    = rgb(235, 86, 96)
	statPurple = rgb(190, 105, 238)
)

func levelTier(v int64) int {
	if v >= 70 {
		return 4
	}
	if v >= 50 {
		return 3
	}
	if v >= 30 {
		return 2
	}
	if v >= 20 {
		return 1
	}
	return 0
}

func fkdrTier(v float64, infinite bool) int {
	if infinite || v >= 15 {
		return 4
	}
	if v >= 8 {
		return 3
	}
	if v >= 5 {
		return 2
	}
	if v >= 3 {
		return 1
	}
	return 0
}

func finalsTier(v int64) int {
	if v >= 15000 {
		return 4
	}
	if v >= 5000 {
		return 3
	}
	if v >= 3000 {
		return 2
	}
	if v >= 1000 {
		return 1
	}
	return 0
}

func bedsTier(v int64) int {
	if v >= 7500 {
		return 4
	}
	if v >= 3000 {
		return 3
	}
	if v >= 1500 {
		return 2
	}
	if v >= 500 {
		return 1
	}
	return 0
}

func winsTier(v int64) int {
	if v >= 5000 {
		return 4
	}
	if v >= 3000 {
		return 3
	}
	if v >= 1000 {
		return 2
	}
	if v >= 500 {
		return 1
	}
	return 0
}

func levelColor(v int64) uintptr {
	switch levelTier(v) {
	case 4:
		return statPurple
	case 3:
		return statRed
	case 2:
		return statOrange
	case 1:
		return statGreen
	default:
		return statGray
	}
}

// A [!] row marks a player whose color-tier combination is dangerous.
// Tiers are hierarchical: red also counts as orange-or-better. The requested
// rules are:
//   - any purple stat; OR
//   - 3+ red stats; OR
//   - at least 1 red stat and at least 3 stats that are orange-or-better.
//
// The last rule includes examples such as 1 green + 2 orange + 1 red.
func isDangerous(p *PlayerStats) bool {
	if p == nil || p.State != "ok" {
		return false
	}
	tiers := []int{
		levelTier(p.Level),
		fkdrTier(p.FKDR, p.Infinite),
		finalsTier(p.FinalKills),
		bedsTier(p.Beds),
		winsTier(p.Wins),
	}
	// Purple is an unconditional danger flag. This means AT LEAST one purple
	// stat: 1, 2, 3, 4 or 5 purple stats all highlight the player.
	for _, t := range tiers {
		if t >= 4 {
			return true
		}
	}

	redOrHigher := 0
	orangeOrHigher := 0
	for _, t := range tiers {
		if t >= 3 {
			redOrHigher++
		}
		if t >= 2 {
			orangeOrHigher++
		}
	}
	if redOrHigher >= 3 {
		return true
	}
	return redOrHigher >= 1 && orangeOrHigher >= 3
}

// ALT is a heuristic warning, not proof. The strongest signal is a mismatch:
// very high FKDR while the account's volume stats are still only gray/green.
// That catches accounts such as ~10 FKDR with only a few hundred wins instead
// of requiring an arbitrarily tiny Pika level.
func isLikelyAlt(p *PlayerStats) bool {
	if p == nil || p.State != "ok" {
		return false
	}

	ft := finalsTier(p.FinalKills)
	bt := bedsTier(p.Beds)
	wt := winsTier(p.Wins)
	lt := levelTier(p.Level)
	highFKDR := p.Infinite || p.FKDR >= 5
	veryHighFKDR := p.Infinite || p.FKDR >= 8

	// Main ALT rule requested by the user: orange-or-better FKDR paired with
	// gray/green account-volume stats. A red/purple FKDR is suspicious even
	// when the level itself has already climbed a little higher.
	lowVolume := ft <= 1 && bt <= 1 && wt <= 1
	if veryHighFKDR && lowVolume {
		return true
	}
	if highFKDR && lowVolume && lt <= 1 {
		return true
	}

	// Also catch a strong mismatch when two of the three volume stats are still
	// gray/green and FKDR is exceptionally high.
	lowCount := 0
	if ft <= 1 {
		lowCount++
	}
	if bt <= 1 {
		lowCount++
	}
	if wt <= 1 {
		lowCount++
	}
	if veryHighFKDR && lowCount >= 2 && p.Level < 50 {
		return true
	}

	// Keep the original low-level / efficiency checks as secondary evidence.
	if p.Level <= 15 && (p.FinalKills >= 1000 || p.Beds >= 500 || p.Wins >= 300 || veryHighFKDR) {
		return true
	}
	if p.GamesPlayed < 20 {
		return false
	}
	games := float64(p.GamesPlayed)
	finalsPerGame := float64(p.FinalKills) / games
	winRate := float64(p.Wins) / games
	if highFKDR && p.Level < 30 && finalsPerGame >= 1.5 {
		return true
	}
	if p.GamesPlayed <= 100 && p.Losses <= 10 && (winRate >= 0.55 || finalsPerGame >= 2.5 || highFKDR) {
		return true
	}
	return false
}

func threatScore(p *PlayerStats) int {
	if p == nil || p.State != "ok" {
		return -1
	}
	score := levelTier(p.Level)*2 + fkdrTier(p.FKDR, p.Infinite)*5 + finalsTier(p.FinalKills)*4 + bedsTier(p.Beds)*3 + winsTier(p.Wins)*4
	if isDangerous(p) {
		score += 100
	}
	if isLikelyAlt(p) {
		score += 40
	}
	return score
}

func fkdrColor(v float64, infinite bool) uintptr {
	if infinite || v >= 15 {
		return statPurple
	}
	if v >= 8 {
		return statRed
	}
	if v >= 5 {
		return statOrange
	}
	if v >= 3 {
		return statGreen
	}
	return statGray
}

func finalsColor(v int64) uintptr {
	if v >= 15000 {
		return statPurple
	}
	if v >= 5000 {
		return statRed
	}
	if v >= 3000 {
		return statOrange
	}
	if v >= 1000 {
		return statGreen
	}
	return statGray
}

func bedsColor(v int64) uintptr {
	// Beds are naturally lower than finals, so these tiers are intentionally
	// lower while keeping the same gray/green/orange/red/purple progression.
	if v >= 7500 {
		return statPurple
	}
	if v >= 3000 {
		return statRed
	}
	if v >= 1500 {
		return statOrange
	}
	if v >= 500 {
		return statGreen
	}
	return statGray
}

func winsColor(v int64) uintptr {
	if v >= 5000 {
		return statPurple
	}
	if v >= 3000 {
		return statRed
	}
	if v >= 1000 {
		return statOrange
	}
	if v >= 500 {
		return statGreen
	}
	if v < 100 {
		return statGray
	}
	// The user only specified gray below 100 and green from 500 upward.
	// Keep the 100-499 middle band neutral instead of inventing another tier.
	return rgb(218, 223, 232)
}

func makeFont(px int32, weight int32) uintptr {
	face := utf16p("Segoe UI")
	r, _, _ := procCreateFontW.Call(uintptr(-px), 0, 0, 0, uintptr(weight), 0, 0, 0, 1, 0, 0, 5, 0, uintptr(unsafe.Pointer(face)))
	return r
}
func del(obj uintptr) {
	if obj != 0 {
		procDeleteObject.Call(obj)
	}
}
func fillRect(hdc uintptr, l, t, r, b int32, c uintptr) {
	br, _, _ := procCreateSolidBrush.Call(c)
	old, _, _ := procSelectObject.Call(hdc, br)
	pen, _, _ := procCreatePen.Call(PS_SOLID, 1, c)
	oldp, _, _ := procSelectObject.Call(hdc, pen)
	procRectangle.Call(hdc, uintptr(l), uintptr(t), uintptr(r), uintptr(b))
	procSelectObject.Call(hdc, oldp)
	procSelectObject.Call(hdc, old)
	del(pen)
	del(br)
}
func drawRoundBox(hdc uintptr, l, t, r, b, rad int32, fill, border uintptr) {
	br, _, _ := procCreateSolidBrush.Call(fill)
	pen, _, _ := procCreatePen.Call(PS_SOLID, 1, border)
	oldb, _, _ := procSelectObject.Call(hdc, br)
	oldp, _, _ := procSelectObject.Call(hdc, pen)
	procRoundRect.Call(hdc, uintptr(l), uintptr(t), uintptr(r), uintptr(b), uintptr(rad), uintptr(rad))
	procSelectObject.Call(hdc, oldp)
	procSelectObject.Call(hdc, oldb)
	del(pen)
	del(br)
}
func line(hdc uintptr, x1, y1, x2, y2 int32, c uintptr) {
	pen, _, _ := procCreatePen.Call(PS_SOLID, 1, c)
	old, _, _ := procSelectObject.Call(hdc, pen)
	procMoveToEx.Call(hdc, uintptr(x1), uintptr(y1), 0)
	procLineTo.Call(hdc, uintptr(x2), uintptr(y2))
	procSelectObject.Call(hdc, old)
	del(pen)
}
func text(hdc, font uintptr, x, y int32, s string, c uintptr) {
	old, _, _ := procSelectObject.Call(hdc, font)
	procSetTextColor.Call(hdc, c)
	u, _ := syscall.UTF16FromString(s)
	if len(u) > 0 {
		procTextOutW.Call(hdc, uintptr(x), uintptr(y), uintptr(unsafe.Pointer(&u[0])), uintptr(len(u)-1))
	}
	procSelectObject.Call(hdc, old)
}
func blend(a, b uintptr, pct int) uintptr {
	ar := int(a & 255)
	ag := int((a >> 8) & 255)
	ab := int((a >> 16) & 255)
	br := int(b & 255)
	bg := int((b >> 8) & 255)
	bb := int((b >> 16) & 255)
	q := 100 - pct
	return rgb(byte((ar*pct+br*q)/100), byte((ag*pct+bg*q)/100), byte((ab*pct+bb*q)/100))
}
func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

func watchLunarLog() {
	// BloodLust supports more than one Minecraft/Lunar log location. Lunar
	// installations can also leave an old multiver latest.log behind while a
	// different instance is the one actually receiving new chat. Instead of
	// picking one path forever, watch every plausible log and let whichever
	// file is actively growing drive the overlay.
	cursors := make(map[string]*logCursor)
	lastDiscovery := time.Time{}

	for {
		if time.Since(lastDiscovery) >= 4*time.Second {
			for _, path := range discoverLogCandidates() {
				if _, ok := cursors[path]; ok {
					continue
				}
				if fi, err := os.Stat(path); err == nil && !fi.IsDir() {
					// Current Lunar can rotate/change profile log locations. For a log
					// that is actively being written, backfill a small recent tail so we
					// do not miss the Pika connection/TAB line during startup discovery.
					off := fi.Size()
					if time.Since(fi.ModTime()) < 90*time.Second {
						const backfill = int64(32 * 1024)
						if off > backfill {
							off -= backfill
						} else {
							off = 0
						}
					}
					cursors[path] = &logCursor{Offset: off}
					learnLocalUsernameFromFile(path)
					debugf("watching candidate log: %s (size=%d offset=%d modified=%s)", path, fi.Size(), off, fi.ModTime().Format(time.RFC3339))
				}
			}
			lastDiscovery = time.Now()
			if len(cursors) == 0 {
				setStatus("Waiting for Lunar Client log...")
			}
		}

		for path, cur := range cursors {
			fi, err := os.Stat(path)
			if err != nil {
				continue
			}
			size := fi.Size()
			if size < cur.Offset {
				cur.Offset = 0
				cur.Partial = ""
			}
			if size <= cur.Offset {
				continue
			}

			f, err := os.Open(path)
			if err != nil {
				continue
			}
			_, _ = f.Seek(cur.Offset, io.SeekStart)
			data, _ := io.ReadAll(io.LimitReader(f, size-cur.Offset))
			_ = f.Close()
			cur.Offset = size
			if len(data) == 0 {
				continue
			}

			// This is the important bit: whichever candidate actually receives
			// new bytes becomes the active log displayed in the footer.
			state.Lock()
			if state.LogPath != path {
				state.LogPath = path
				if !state.Pika {
					state.Status = "Active Minecraft log found • waiting for Pika/TAB roster"
				}
				debugf("active log switched to: %s", path)
			}
			state.Unlock()

			chunk := cur.Partial + string(data)
			logLines := strings.Split(chunk, "\n")
			if len(logLines) > 0 {
				cur.Partial = logLines[len(logLines)-1]
				logLines = logLines[:len(logLines)-1]
			}
			for _, ln := range logLines {
				processLogLine(strings.TrimRight(ln, "\r"))
			}
			notifyRefresh()
		}
		time.Sleep(180 * time.Millisecond)
	}
}

func discoverLogCandidates() []string {
	home, _ := os.UserHomeDir()
	appdata := os.Getenv("APPDATA")
	seen := map[string]bool{}
	out := make([]string, 0, 12)
	add := func(p string) {
		if p == "" {
			return
		}
		p = filepath.Clean(p)
		k := strings.ToLower(p)
		if seen[k] {
			return
		}
		if fi, err := os.Stat(p); err == nil && !fi.IsDir() {
			seen[k] = true
			out = append(out, p)
		}
	}

	if home != "" {
		// Current Lunar launcher profiles (1.8.9 is stored under the 1.8 profile).
		add(filepath.Join(home, ".lunarclient", "profiles", "lunar", "1.8", "logs", "latest.log"))
		add(filepath.Join(home, ".lunarclient", "profiles", "lunar", "1.8.9", "logs", "latest.log"))
		// Legacy Lunar locations used by older launchers / BloodLust.
		add(filepath.Join(home, ".lunarclient", "offline", "multiver", "logs", "latest.log"))
		add(filepath.Join(home, ".lunarclient", "offline", "1.8", "logs", "latest.log"))
		add(filepath.Join(home, ".lunarclient", "logs", "latest.log"))

		// Current Lunar can keep multiple named profiles. Scan only the profiles
		// tree for logs/latest.log; this is small and avoids touching asset caches.
		profilesRoot := filepath.Join(home, ".lunarclient", "profiles")
		_ = filepath.WalkDir(profilesRoot, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if d.IsDir() {
				n := strings.ToLower(d.Name())
				if n == "assets" || n == "cache" || n == "textures" || n == "natives" || n == "resourcepacks" {
					return filepath.SkipDir
				}
				return nil
			}
			if strings.EqualFold(d.Name(), "latest.log") && strings.EqualFold(filepath.Base(filepath.Dir(path)), "logs") {
				add(path)
			}
			return nil
		})

		// Lunar has used more than one offline instance directory over time.
		// Discover only latest.log files inside its offline tree. This walk runs
		// periodically, not every render/log tick, and the tree is tiny
		// compared with scanning the whole disk.
		root := filepath.Join(home, ".lunarclient", "offline")
		_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if d.IsDir() {
				// Keep the scan bounded; caches/assets are irrelevant.
				n := strings.ToLower(d.Name())
				if n == "assets" || n == "cache" || n == "textures" || n == "natives" {
					return filepath.SkipDir
				}
				return nil
			}
			if strings.EqualFold(d.Name(), "latest.log") && strings.EqualFold(filepath.Base(filepath.Dir(path)), "logs") {
				add(path)
			}
			return nil
		})
	}

	// BloodLust also includes the normal .minecraft log path. This matters for
	// some Lunar/Forge setups where the file receiving chat is not multiver.
	if appdata != "" {
		add(filepath.Join(appdata, ".minecraft", "logs", "latest.log"))
		add(filepath.Join(appdata, ".minecraft", "logs", "blclient", "minecraft", "latest.log"))
	}
	return out
}

func watchToggleKey() {
	wasDown := false
	for {
		r, _, _ := procGetAsyncKeyState.Call(VK_X)
		down := int16(r&0xffff) < 0
		if down && !wasDown {
			state.RLock()
			hwnd := state.HWND
			state.RUnlock()
			if hwnd != 0 {
				procPostMessageW.Call(hwnd, WM_APP_TOGGLE, 0, 0)
			}
		}
		wasDown = down
		time.Sleep(18 * time.Millisecond)
	}
}

func watchTabKey() {
	wasDown := false
	for {
		r, _, _ := procGetAsyncKeyState.Call(VK_TAB)
		down := int16(r&0xffff) < 0
		if down && !wasDown {
			now := time.Now()
			debugf("TAB pressed; using roster candidate from just before/after TAB")
			state.Lock()
			state.ScanUntil = now.Add(4 * time.Second)
			state.Status = "TAB pressed • reading player roster..."
			state.Unlock()

			rosterCandidateMu.Lock()
			lastTabAt = now
			cached := append([]string(nil), rosterCandidate...)
			cachedAt := rosterCandidateAt
			rosterCandidateMu.Unlock()
			// Minecraft can write the autocomplete line before our 20ms key poll
			// notices TAB. Accept a candidate captured just before this event.
			if len(cached) >= 3 && now.Sub(cachedAt) >= 0 && now.Sub(cachedAt) <= 1200*time.Millisecond {
				debugf("using pre-TAB cached roster (%d): %s", len(cached), strings.Join(cached, ", "))
				setRoster(cached)
				state.Lock()
				state.ScanUntil = time.Time{}
				state.Unlock()
				rosterCandidateMu.Lock()
				lastTabAt = time.Time{}
				rosterCandidateMu.Unlock()
			}
			notifyRefresh()
		}
		wasDown = down
		time.Sleep(12 * time.Millisecond)
	}
}

func processLogLine(line string) {
	learnLocalUsernameFromLine(line)
	low := strings.ToLower(line)

	if strings.Contains(low, "connecting to") {
		if strings.Contains(low, "pika-network") || strings.Contains(low, "pikanetwork") || strings.Contains(low, "pika.host") {
			markPika()
			clearPlayers()
		} else if strings.Contains(low, ".net") || strings.Contains(low, ".com") || strings.Contains(low, "localhost") {
			state.Lock()
			state.Pika = false
			state.BedWars = false
			state.GameLive = false
			state.ExpectedPlayers = 0
			state.Status = "Connected to another server"
			state.Unlock()
			clearPlayers()
			notifyRefresh()
		}
	}

	if strings.Contains(low, "pika-network.net") || strings.Contains(low, "play.pika-network.net") || strings.Contains(low, "pikanetwork") || strings.Contains(low, "pika.host") {
		markPika()
	}

	if strings.Contains(low, "bedwars") || strings.Contains(low, "bed wars") {
		state.Lock()
		changed := !state.BedWars
		state.BedWars = true
		if state.Pika && changed {
			state.Status = "Pika BedWars • T → Space → TAB to scan roster"
		}
		state.Unlock()
		if changed {
			autoShow()
			notifyRefresh()
		}
	}

	if strings.Contains(low, "the game will start in 1 second") || strings.Contains(low, "game has started") {
		state.Lock()
		changed := !state.GameLive
		state.BedWars = true
		state.GameLive = true
		if state.Pika && changed {
			state.Status = "BedWars started • TAB scan refreshes roster"
		}
		state.Unlock()
		if changed {
			notifyRefresh()
		}
	}

	// Join/quit text is NOT used for identities. It is only used as an
	// optional sanity-check for the current queue size, e.g. (7/8).
	if strings.Contains(low, "has joined") || strings.Contains(low, "has quit") {
		if cur, ok := extractQueueCount(line); ok {
			state.Lock()
			state.ExpectedPlayers = cur
			state.Unlock()
		}
	}

	// PRIMARY roster source: Minecraft's chat TAB-completion output.
	// BloodLust continuously regex-scans log lines instead of waiting for a
	// keyboard event. We do the same, while using TAB only as a confidence hint.
	state.RLock()
	scanActive := time.Now().Before(state.ScanUntil)
	pika := state.Pika
	bw := state.BedWars
	state.RUnlock()

	pure := extractPureTabRoster(line)
	loose := extractBloodLustRoster(line)
	candidate := pure
	if len(candidate) < 3 {
		candidate = loose
	}
	if len(candidate) >= 3 {
		rosterCandidateMu.Lock()
		rosterCandidate = append(rosterCandidate[:0], candidate...)
		rosterCandidateAt = time.Now()
		tabAgo := time.Since(lastTabAt)
		rosterCandidateMu.Unlock()

		// Never apply a roster merely because an old pure comma-separated line
		// exists in latest.log. Only a candidate around the user's explicit TAB
		// press is allowed to replace the current roster. This prevents startup
		// backfill from replaying dozens of historical queues and spawning a burst
		// of API/avatar work.
		aroundTab := scanActive || (tabAgo >= 0 && tabAgo <= 1500*time.Millisecond)
		accept := pika && bw && aroundTab && (len(pure) >= 3 || len(loose) >= 3)
		if aroundTab || accept {
			debugf("roster candidate (%d, pure=%t, pika=%t, bw=%t, scan=%t): %s", len(candidate), len(pure) >= 3, pika, bw, scanActive, strings.Join(candidate, ", "))
		}
		if accept {
			setRoster(candidate)
			state.Lock()
			state.ScanUntil = time.Time{}
			state.Unlock()
			// Consume this TAB event so additional log lines from the same completion
			// burst cannot repeatedly replace/requeue the roster.
			rosterCandidateMu.Lock()
			lastTabAt = time.Time{}
			rosterCandidateMu.Unlock()
		}
	} else if scanActive && strings.Contains(line, "[CHAT]") {
		sample := line
		if len(sample) > 600 {
			sample = sample[:600]
		}
		debugf("TAB-window chat: %s", sample)
	}

	// Chat command feature: only react to this client's own echoed chat line.
	// The standalone overlay does not intercept/suppress Minecraft chat; after
	// the user's -stats command is echoed by Pika, it sends the result as a
	// second normal chat message from the same Minecraft account.
	maybeHandleStatsCommand(line)
}

func learnLocalUsernameFromLine(line string) {
	m := settingUserRE.FindStringSubmatch(line)
	if len(m) != 2 || !validUsername(m[1]) {
		return
	}
	identityMu.Lock()
	changed := !strings.EqualFold(localUsername, m[1])
	localUsername = m[1]
	identityMu.Unlock()
	if changed {
		debugf("detected local Minecraft username: %s", m[1])
	}
}

func learnLocalUsernameFromFile(path string) {
	identityMu.RLock()
	known := localUsername != ""
	identityMu.RUnlock()
	if known {
		return
	}
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()
	// Lunar writes "Setting user: <name>" near startup, so the first 512 KiB
	// is enough even when latest.log has grown very large during a long session.
	data, err := io.ReadAll(io.LimitReader(f, 512*1024))
	if err != nil {
		return
	}
	m := settingUserRE.FindStringSubmatch(string(data))
	if len(m) == 2 && validUsername(m[1]) {
		identityMu.Lock()
		localUsername = m[1]
		identityMu.Unlock()
		debugf("detected local Minecraft username from log header: %s", m[1])
	}
}

func getLocalUsername() string {
	identityMu.RLock()
	defer identityMu.RUnlock()
	return localUsername
}

func usernameTokenPresent(s, want string) bool {
	if want == "" {
		return false
	}
	for _, tok := range usernameRE.FindAllString(s, -1) {
		if strings.EqualFold(tok, want) {
			return true
		}
	}
	return false
}

func maybeHandleStatsCommand(line string) {
	if !strings.Contains(line, "[CHAT]") {
		return
	}
	state.RLock()
	pika := state.Pika
	state.RUnlock()
	if !pika {
		return
	}

	payload := chatPayload(line)
	m := statsCommandRE.FindStringSubmatch(payload)
	if len(m) != 2 || !validUsername(m[1]) {
		return
	}

	cmdPos := strings.LastIndex(strings.ToLower(payload), "-stats")
	if cmdPos < 0 {
		return
	}
	me := getLocalUsername()
	if me == "" || !usernameTokenPresent(payload[:cmdPos], me) {
		// Do not turn the overlay into a public bot that answers everybody.
		// It only responds when *this user's* own chat echo contains -stats.
		return
	}

	target := m[1]
	sig := strings.ToLower(me + "|" + target)
	chatCommandMu.Lock()
	if sig == lastChatCommand && time.Since(lastChatCommandAt) < 4*time.Second {
		chatCommandMu.Unlock()
		return
	}
	lastChatCommand = sig
	lastChatCommandAt = time.Now()
	chatCommandMu.Unlock()

	debugf("own -stats command detected: requested=%s", target)
	state.Lock()
	state.Status = "Chat stats request: " + target
	state.Unlock()
	notifyRefresh()
	go answerStatsCommand(target)
}

func answerStatsCommand(target string) {
	key := strings.ToLower(target)
	var stats *PlayerStats
	cacheMu.Lock()
	if c, ok := cache[key]; ok && time.Since(c.Updated) < 10*time.Minute && (c.State == "ok" || c.State == "unavailable") {
		cp := *c
		stats = &cp
	}
	cacheMu.Unlock()

	if stats == nil {
		stats = fetchStats(target)
		stats.Updated = time.Now()
		cacheMu.Lock()
		cp := *stats
		cache[key] = &cp
		cacheMu.Unlock()
	}

	msg := formatStatsChatMessage(target, stats)
	// Let Minecraft fully close the chat screen used to send -stats before we
	// perform the automatic response. This avoids the old race that could
	// leave a GUI/chat window in a weird state.
	time.Sleep(320 * time.Millisecond)
	if err := sendMinecraftChat(msg); err != nil {
		debugf("chat send failed for %s: %v", target, err)
		state.Lock()
		state.Status = "Stats ready, but Minecraft was not focused"
		state.Unlock()
		notifyRefresh()
		return
	}
	debugf("chat stats sent for %s: %s", target, msg)
	state.Lock()
	state.Status = "Sent " + target + " stats in chat"
	state.Unlock()
	notifyRefresh()
}

func formatStatsChatMessage(target string, s *PlayerStats) string {
	if s == nil || s.State == "api" {
		return fmt.Sprintf("%s stats ; API unavailable, try again.", target)
	}
	if s.State == "unavailable" {
		return fmt.Sprintf("%s stats ; Stats unavailable.", target)
	}
	fk := fmt.Sprintf("%.2f", s.FKDR)
	if s.Infinite {
		fk = "INF"
	}
	msg := fmt.Sprintf("%s stats ; FKDR: %s, FINAL KILLS: %s, BEDS: %s, WINS: %s", target, fk, comma(s.FinalKills), comma(s.Beds), comma(s.Wins))
	// Minecraft 1.8.x chat input is intentionally short. Keep a compact
	// fallback so even unusually large stat values fit safely.
	if len([]rune(msg)) > 98 {
		msg = fmt.Sprintf("%s stats ; FKDR:%s FINALS:%d BEDS:%d WINS:%d", target, fk, s.FinalKills, s.Beds, s.Wins)
	}
	return msg
}

func sendMinecraftChat(message string) error {
	chatSendMu.Lock()
	defer chatSendMu.Unlock()

	deadline := time.Now().Add(2500 * time.Millisecond)
	for !minecraftIsForeground() && time.Now().Before(deadline) {
		time.Sleep(60 * time.Millisecond)
	}
	if !minecraftIsForeground() {
		return fmt.Errorf("Minecraft/Lunar is not the foreground window")
	}

	// Standalone/no-injection mode: open Minecraft chat, insert the entire
	// response in one SendInput batch, and immediately press Enter. The text is
	// no longer typed character-by-character and TAB is never sent.
	if !sendVirtualKey(VK_T) {
		return fmt.Errorf("could not open chat")
	}
	time.Sleep(65 * time.Millisecond)
	if err := sendUnicodeTextBatch(message); err != nil {
		return err
	}
	time.Sleep(20 * time.Millisecond)
	if !sendVirtualKey(VK_RETURN) {
		return fmt.Errorf("could not press Enter")
	}
	return nil
}

func minecraftIsForeground() bool {
	hwnd, _, _ := procGetForegroundWindow.Call()
	if hwnd == 0 {
		return false
	}
	buf := make([]uint16, 256)
	n, _, _ := procGetWindowTextW.Call(hwnd, uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
	if n == 0 {
		return false
	}
	title := strings.ToLower(syscall.UTF16ToString(buf[:n]))
	return strings.Contains(title, "minecraft") || strings.Contains(title, "lunar")
}

func keyInput(vk, scan uint16, flags uint32) INPUT {
	var in INPUT
	in.Type = INPUT_KEYBOARD
	ki := (*KEYBDINPUT)(unsafe.Pointer(&in.Data[0]))
	ki.WVk = vk
	ki.WScan = scan
	ki.DwFlags = flags
	return in
}

func sendInputs(inputs []INPUT) bool {
	if len(inputs) == 0 {
		return true
	}
	r, _, _ := procSendInput.Call(uintptr(len(inputs)), uintptr(unsafe.Pointer(&inputs[0])), unsafe.Sizeof(INPUT{}))
	return int(r) == len(inputs)
}

func sendVirtualKey(vk uint16) bool {
	inputs := []INPUT{
		keyInput(vk, 0, 0),
		keyInput(vk, 0, KEYEVENTF_KEYUP),
	}
	return sendInputs(inputs)
}

func sendUnicodeTextBatch(s string) error {
	u, err := syscall.UTF16FromString(s)
	if err != nil {
		return err
	}
	inputs := make([]INPUT, 0, len(u)*2)
	for _, ch := range u {
		if ch == 0 {
			break
		}
		inputs = append(inputs,
			keyInput(0, ch, KEYEVENTF_UNICODE),
			keyInput(0, ch, KEYEVENTF_UNICODE|KEYEVENTF_KEYUP),
		)
	}
	if !sendInputs(inputs) {
		return fmt.Errorf("SendInput failed while inserting chat response")
	}
	return nil
}

func extractQueueCount(line string) (int, bool) {
	re := regexp.MustCompile(`\((\d{1,2})/(\d{1,2})\)`)
	m := re.FindStringSubmatch(line)
	if len(m) != 3 {
		return 0, false
	}
	n, err := strconv.Atoi(m[1])
	if err != nil || n < 0 || n > maxPlayers {
		return 0, false
	}
	return n, true
}

func chatPayload(line string) string {
	clean := mcColorRE.ReplaceAllString(line, "")
	if idx := strings.LastIndex(clean, "[CHAT]"); idx >= 0 {
		return strings.TrimSpace(clean[idx+len("[CHAT]"):])
	}
	return strings.TrimSpace(clean)
}

func extractPureTabRoster(line string) []string {
	payload := chatPayload(line)
	if payload == "" || !strings.Contains(payload, ", ") {
		return nil
	}
	parts := strings.Split(payload, ", ")
	if len(parts) < 3 || len(parts) > maxPlayers+2 {
		return nil
	}
	names := make([]string, 0, len(parts))
	seen := make(map[string]bool)
	for _, raw := range parts {
		n := strings.TrimSpace(raw)
		if !validUsername(n) || blacklistedName(n) {
			return nil
		}
		key := strings.ToLower(n)
		if seen[key] {
			return nil
		}
		seen[key] = true
		names = append(names, n)
	}
	return names
}

var bloodLustSequenceRE = regexp.MustCompile(`(?:^|[^A-Za-z0-9_])([A-Za-z0-9_]{3,16}(?:, [A-Za-z0-9_]{3,16}){2,})(?:$|[^A-Za-z0-9_])`)

func extractBloodLustRoster(line string) []string {
	payload := chatPayload(line)
	if payload == "" || !strings.Contains(payload, ", ") {
		return nil
	}
	m := bloodLustSequenceRE.FindStringSubmatch(payload)
	if len(m) < 2 {
		return nil
	}
	parts := strings.Split(m[1], ", ")
	if len(parts) < 3 || len(parts) > maxPlayers {
		return nil
	}
	names := make([]string, 0, len(parts))
	seen := map[string]bool{}
	for _, n := range parts {
		n = strings.TrimSpace(n)
		if !validUsername(n) || blacklistedName(n) {
			return nil
		}
		k := strings.ToLower(n)
		if seen[k] {
			return nil
		}
		seen[k] = true
		names = append(names, n)
	}
	return names
}

func markPika() {
	state.Lock()
	was := state.Pika
	state.Pika = true
	if state.Status == "" || !was {
		state.Status = "PikaNetwork detected • press T, Space, TAB to scan players"
	}
	state.Unlock()
	autoShow()
}
func validUsername(s string) bool {
	if len(s) < 3 || len(s) > 16 {
		return false
	}
	for _, r := range s {
		if !(r == '_' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9') {
			return false
		}
	}
	return true
}
func blacklistedName(s string) bool {
	switch strings.ToLower(s) {
	case "client", "thread", "info", "chat", "minecraft", "lunar", "bedwars", "server", "players", "loading", "unknown", "hypixel", "pikanetwork", "name", "support":
		return true
	}
	return false
}

func addPlayer(name string) {
	if !validUsername(name) || blacklistedName(name) {
		return
	}
	key := strings.ToLower(name)
	state.Lock()
	if _, ok := state.Players[key]; !ok {
		state.Players[key] = &PlayerStats{Username: name, State: "loading"}
		state.Order = append(state.Order, key)
		if len(state.Order) > maxPlayers {
			old := state.Order[0]
			delete(state.Players, old)
			state.Order = state.Order[1:]
		}
	}
	state.BedWars = true
	state.Status = "BedWars players detected"
	state.Unlock()
	queueStats(name)
	queueAvatar(name)
	autoShow()
	notifyRefresh()
}
func removePlayer(name string) {
	key := strings.ToLower(name)
	state.Lock()
	delete(state.Players, key)
	out := state.Order[:0]
	for _, k := range state.Order {
		if k != key {
			out = append(out, k)
		}
	}
	state.Order = out
	state.Unlock()
	notifyRefresh()
}
func clearPlayers() {
	state.Lock()
	state.Players = make(map[string]*PlayerStats)
	state.Order = nil
	state.GameLive = false
	state.Unlock()
	notifyRefresh()
}
func setRoster(names []string) {
	// Normalize and de-duplicate the completion result while preserving order.
	clean := make([]string, 0, len(names))
	seen := map[string]bool{}
	for _, n := range names {
		if !validUsername(n) {
			continue
		}
		key := strings.ToLower(n)
		if seen[key] {
			continue
		}
		seen[key] = true
		clean = append(clean, n)
		if len(clean) >= maxPlayers {
			break
		}
	}
	if len(clean) < 1 {
		return
	}

	state.Lock()
	// If TAB produced the exact same roster, keep the existing rows/cache and do
	// not requeue network/head work or trigger a resize storm.
	same := len(state.Order) == len(clean)
	if same {
		for _, n := range clean {
			if _, ok := state.Players[strings.ToLower(n)]; !ok {
				same = false
				break
			}
		}
	}
	if same {
		state.BedWars = true
		state.Status = fmt.Sprintf("TAB roster refreshed: %d players", len(clean))
		state.Unlock()
		autoShow()
		notifyRefresh()
		return
	}

	seen = map[string]bool{}
	for _, n := range clean {
		key := strings.ToLower(n)
		seen[key] = true
		if _, ok := state.Players[key]; !ok {
			state.Players[key] = &PlayerStats{Username: n, State: "loading"}
			state.Order = append(state.Order, key)
		}
	}
	out := make([]string, 0, len(clean))
	for _, k := range state.Order {
		if seen[k] {
			out = append(out, k)
		} else {
			delete(state.Players, k)
		}
	}
	state.Order = out
	state.BedWars = true
	state.Status = fmt.Sprintf("TAB roster captured: %d players • fetching stats", len(clean))
	state.Unlock()
	for _, n := range clean {
		queueStats(n)
		queueAvatar(n)
	}
	autoShow()
	notifyRefresh()
}
func queueAvatar(name string) {
	key := strings.ToLower(name)
	avatarMu.RLock()
	_, ok := avatars[key]
	avatarMu.RUnlock()
	if ok {
		return
	}
	avatarQueuedMu.Lock()
	if avatarQueued[key] {
		avatarQueuedMu.Unlock()
		return
	}
	avatarQueued[key] = true
	avatarQueuedMu.Unlock()
	select {
	case avatarQueue <- name:
	default:
		avatarQueuedMu.Lock()
		delete(avatarQueued, key)
		avatarQueuedMu.Unlock()
	}
}

func avatarWorker() {
	client := &http.Client{Timeout: 4 * time.Second}
	for name := range avatarQueue {
		key := strings.ToLower(name)
		av := fetchAvatar(client, name)
		avatarMu.Lock()
		// Cache nil as an empty sentinel so unresolved/cracked names are not
		// requested repeatedly in the same overlay session.
		avatars[key] = av
		avatarMu.Unlock()
		avatarQueuedMu.Lock()
		delete(avatarQueued, key)
		avatarQueuedMu.Unlock()
		if av != nil {
			notifyRefresh()
		}
	}
}

func fetchAvatar(client *http.Client, name string) *Avatar {
	// MCHeads accepts a Minecraft username as an identifier. Requesting a tiny
	// PNG keeps bandwidth and decode work negligible. This is deliberately
	// best-effort because Pika also supports offline/cracked usernames.
	url := "https://mc-heads.net/avatar/" + name + "/32"
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil
	}
	req.Header.Set("User-Agent", "PikaStatsOverlay/"+appVersion)
	resp, err := client.Do(req)
	if err != nil {
		debugf("avatar %s request failed: %v", name, err)
		return nil
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		debugf("avatar %s http=%d", name, resp.StatusCode)
		return nil
	}
	img, err := png.Decode(io.LimitReader(resp.Body, 256*1024))
	if err != nil {
		debugf("avatar %s decode failed: %v", name, err)
		return nil
	}
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	if w <= 0 || h <= 0 || w > 128 || h > 128 {
		return nil
	}
	pix := make([]byte, w*h*4)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			r, g, bb, a := img.At(b.Min.X+x, b.Min.Y+y).RGBA()
			i := (y*w + x) * 4
			pix[i+0] = byte(bb >> 8)
			pix[i+1] = byte(g >> 8)
			pix[i+2] = byte(r >> 8)
			pix[i+3] = byte(a >> 8)
		}
	}
	return &Avatar{Width: int32(w), Height: int32(h), Pixels: pix}
}

func getAvatar(name string) *Avatar {
	avatarMu.RLock()
	av := avatars[strings.ToLower(name)]
	avatarMu.RUnlock()
	return av
}

func drawAvatar(hdc uintptr, x, y, w, h int32, av *Avatar) {
	if av == nil || len(av.Pixels) == 0 || av.Width <= 0 || av.Height <= 0 {
		return
	}
	bmi := BITMAPINFO{}
	bmi.BmiHeader.BiSize = uint32(unsafe.Sizeof(BITMAPINFOHEADER{}))
	bmi.BmiHeader.BiWidth = av.Width
	// Negative height tells GDI the pixel buffer is top-down.
	bmi.BmiHeader.BiHeight = -av.Height
	bmi.BmiHeader.BiPlanes = 1
	bmi.BmiHeader.BiBitCount = 32
	bmi.BmiHeader.BiCompression = BI_RGB
	procStretchDIBits.Call(
		hdc,
		uintptr(x), uintptr(y), uintptr(w), uintptr(h),
		0, 0, uintptr(av.Width), uintptr(av.Height),
		uintptr(unsafe.Pointer(&av.Pixels[0])),
		uintptr(unsafe.Pointer(&bmi)),
		DIB_RGB_COLORS, SRCCOPY,
	)
	// Keep Go-backed buffers alive until the native GDI call has returned.
	runtime.KeepAlive(av)
	runtime.KeepAlive(&bmi)
}

func queueStats(name string) {
	key := strings.ToLower(name)
	cacheMu.Lock()
	if c, ok := cache[key]; ok && time.Since(c.Updated) < 10*time.Minute {
		cp := *c
		cacheMu.Unlock()
		applyStats(key, &cp)
		return
	}
	cacheMu.Unlock()
	queuedMu.Lock()
	if queued[key] {
		queuedMu.Unlock()
		return
	}
	queued[key] = true
	queuedMu.Unlock()
	select {
	case requestQueue <- name:
	default:
		queuedMu.Lock()
		delete(queued, key)
		queuedMu.Unlock()
	}
}

func apiWorker() {
	for name := range requestQueue {
		key := strings.ToLower(name)
		stats := fetchStats(name)
		debugf("stats %s -> state=%s level=%d fk=%d fd=%d beds=%d wins=%d games=%d losses=%d", name, stats.State, stats.Level, stats.FinalKills, stats.FinalDeaths, stats.Beds, stats.Wins, stats.GamesPlayed, stats.Losses)
		stats.Updated = time.Now()
		cacheMu.Lock()
		cp := *stats
		cache[key] = &cp
		cacheMu.Unlock()
		queuedMu.Lock()
		delete(queued, key)
		queuedMu.Unlock()
		applyStats(key, stats)
	}
}
func applyStats(key string, s *PlayerStats) {
	state.Lock()
	if cur := state.Players[key]; cur != nil {
		s.Username = cur.Username
		*cur = *s
	}
	state.Unlock()
	notifyRefresh()
}

func fetchStats(name string) *PlayerStats {
	s := &PlayerStats{Username: name, State: "api"}
	profileURL := "https://stats.pika-network.net/api/profile/" + name
	var profile any
	code, err := getJSON(profileURL, &profile)
	if err != nil {
		if code == 404 {
			s.State = "unavailable"
		}
		return s
	}
	if v, ok := getPathNumber(profile, "rank", "level"); ok {
		s.Level = int64(v)
	} else if v, ok := findKeyNumber(profile, "level"); ok {
		s.Level = int64(v)
	}

	lbURL := "https://stats.pika-network.net/api/profile/" + name + "/leaderboard?type=bedwars&interval=total&mode=ALL_MODES"
	var lb any
	code, err = getJSON(lbURL, &lb)
	if err != nil {
		if code == 404 {
			s.State = "unavailable"
		}
		return s
	}
	s.FinalKills = playerLeaderboardValue(lb, "Final kills", name)
	s.FinalDeaths = playerLeaderboardValue(lb, "Final deaths", name)
	s.Beds = playerLeaderboardValue(lb, "Beds destroyed", name)
	s.Wins = playerLeaderboardValue(lb, "Wins", name)
	s.GamesPlayed = playerLeaderboardValue(lb, "Games played", name)
	s.Losses = playerLeaderboardValue(lb, "Losses", name)
	if s.FinalDeaths == 0 {
		if s.FinalKills > 0 {
			s.Infinite = true
		} else {
			s.FKDR = 0
		}
	} else {
		s.FKDR = float64(s.FinalKills) / float64(s.FinalDeaths)
	}
	s.State = "ok"
	return s
}

func getJSON(url string, out any) (int, error) {
	var last error
	for attempt := 0; attempt < 2; attempt++ {
		<-apiRateTicker.C
		req, _ := http.NewRequest("GET", url, nil)
		req.Header.Set("User-Agent", "PikaStatsOverlay/"+appVersion)
		resp, err := httpClient.Do(req)
		if err != nil {
			last = err
			time.Sleep(time.Duration(attempt+1) * 700 * time.Millisecond)
			continue
		}
		code := resp.StatusCode
		if code == 429 {
			io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
			wait := 2 * time.Second
			if h := resp.Header.Get("Retry-After"); h != "" {
				if n, e := strconv.Atoi(h); e == nil && n > 0 && n < 30 {
					wait = time.Duration(n) * time.Second
				}
			}
			time.Sleep(wait)
			last = fmt.Errorf("rate limited")
			continue
		}
		if code == 404 {
			resp.Body.Close()
			return code, fmt.Errorf("not found")
		}
		if code < 200 || code >= 300 {
			io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
			last = fmt.Errorf("http %d", code)
			if code >= 500 {
				time.Sleep(time.Duration(attempt+1) * 700 * time.Millisecond)
				continue
			}
			return code, last
		}
		dec := json.NewDecoder(io.LimitReader(resp.Body, 2*1024*1024))
		err = dec.Decode(out)
		resp.Body.Close()
		if err != nil {
			return code, err
		}
		return code, nil
	}
	return 0, last
}

func playerLeaderboardValue(v any, statName, username string) int64 {
	root, ok := v.(map[string]any)
	if !ok {
		return 0
	}
	var stat any
	for k, val := range root {
		if strings.EqualFold(strings.TrimSpace(k), statName) {
			stat = val
			break
		}
	}
	obj, ok := stat.(map[string]any)
	if !ok {
		return 0
	}
	entries, ok := obj["entries"].([]any)
	if !ok || len(entries) == 0 {
		return 0
	}
	// The API's metadata.total is the GLOBAL number of players/stat records,
	// not this player's value. Only entries[].value is a player statistic.
	for _, raw := range entries {
		e, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if id, _ := e["id"].(string); id != "" && !strings.EqualFold(id, username) {
			continue
		}
		if n, ok := scalarNumber(e["value"]); ok {
			return int64(n)
		}
	}
	// Some responses may omit/normalize the id. Fall back to the first entry,
	// but still never read metadata.total.
	if e, ok := entries[0].(map[string]any); ok {
		if n, ok := scalarNumber(e["value"]); ok {
			return int64(n)
		}
	}
	return 0
}

func scalarNumber(v any) (float64, bool) {
	switch t := v.(type) {
	case float64:
		return t, true
	case json.Number:
		n, err := t.Float64()
		return n, err == nil
	case string:
		n, err := strconv.ParseFloat(strings.ReplaceAll(strings.TrimSpace(t), ",", ""), 64)
		return n, err == nil
	}
	return 0, false
}

func normalizeKey(s string) string {
	return strings.ToLower(strings.ReplaceAll(strings.ReplaceAll(strings.TrimSpace(s), "_", ""), " ", ""))
}
func statInt(v any, name string) int64 {
	if x, ok := findStat(v, name); ok {
		return int64(x)
	}
	return 0
}
func findStat(v any, target string) (float64, bool) {
	want := normalizeKey(target)
	switch t := v.(type) {
	case map[string]any:
		for k, val := range t {
			if normalizeKey(k) == want {
				if n, ok := extractStatNumber(val); ok {
					return n, true
				}
			}
		}
		for _, val := range t {
			if n, ok := findStat(val, target); ok {
				return n, true
			}
		}
	case []any:
		for _, val := range t {
			if n, ok := findStat(val, target); ok {
				return n, true
			}
		}
	}
	return 0, false
}
func extractStatNumber(v any) (float64, bool) {
	switch t := v.(type) {
	case float64:
		return t, true
	case json.Number:
		n, e := t.Float64()
		return n, e == nil
	case string:
		n, e := strconv.ParseFloat(strings.ReplaceAll(t, ",", ""), 64)
		return n, e == nil
	case map[string]any:
		for _, k := range []string{"value", "Value", "amount", "count"} {
			if x, ok := t[k]; ok {
				if n, ok := extractStatNumber(x); ok {
					return n, true
				}
			}
		}
		if e, ok := t["entries"]; ok {
			if n, ok := extractStatNumber(e); ok {
				return n, true
			}
		}
		if e, ok := t["Entries"]; ok {
			if n, ok := extractStatNumber(e); ok {
				return n, true
			}
		}
		for _, x := range t {
			if n, ok := extractStatNumber(x); ok {
				return n, true
			}
		}
	case []any:
		for _, x := range t {
			if n, ok := extractStatNumber(x); ok {
				return n, true
			}
		}
	}
	return 0, false
}
func getPathNumber(v any, path ...string) (float64, bool) {
	cur := v
	for _, p := range path {
		m, ok := cur.(map[string]any)
		if !ok {
			return 0, false
		}
		var next any
		found := false
		for k, val := range m {
			if strings.EqualFold(k, p) {
				next = val
				found = true
				break
			}
		}
		if !found {
			return 0, false
		}
		cur = next
	}
	return extractStatNumber(cur)
}
func findKeyNumber(v any, key string) (float64, bool) {
	switch t := v.(type) {
	case map[string]any:
		for k, val := range t {
			if strings.EqualFold(k, key) {
				if n, ok := extractStatNumber(val); ok {
					return n, true
				}
			}
		}
		for _, val := range t {
			if n, ok := findKeyNumber(val, key); ok {
				return n, true
			}
		}
	case []any:
		for _, val := range t {
			if n, ok := findKeyNumber(val, key); ok {
				return n, true
			}
		}
	}
	return 0, false
}

func comma(n int64) string {
	s := strconv.FormatInt(n, 10)
	start := 0
	if strings.HasPrefix(s, "-") {
		start = 1
	}
	for i := len(s) - 3; i > start; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	return s
}
func setStatus(s string) { state.Lock(); state.Status = s; state.Unlock(); notifyRefresh() }

func configPath() string {
	dir, _ := os.UserConfigDir()
	if dir == "" {
		home, _ := os.UserHomeDir()
		dir = home
	}
	return filepath.Join(dir, "PikaStatsOverlay", "config.json")
}

type config struct {
	X int32 `json:"x"`
	Y int32 `json:"y"`
}

func init() {
	p := configPath()
	b, err := os.ReadFile(p)
	if err == nil {
		var c config
		if json.Unmarshal(b, &c) == nil {
			state.X = c.X
			state.Y = c.Y
		}
	}
}
func saveWindowPosition(hwnd uintptr) {
	var r RECT
	if v, _, _ := procGetWindowRect.Call(hwnd, uintptr(unsafe.Pointer(&r))); v != 0 {
		state.Lock()
		state.X = r.Left
		state.Y = r.Top
		state.Unlock()
		p := configPath()
		_ = os.MkdirAll(filepath.Dir(p), 0755)
		b, _ := json.MarshalIndent(config{X: r.Left, Y: r.Top}, "", "  ")
		_ = os.WriteFile(p, b, 0644)
	}
}

// Keep bufio imported intentionally available for future log-format extensions.
var _ = bufio.ErrInvalidUnreadByte
