package browser

import (
	"fmt"
	"math"
	"strings"
)

// PointerFrame describes where a viewport lies on the latest viewport screenshot
// shown to the model when the screenshot had to be rescaled from CSS pixels.
// upstream: PointerFrame in pointer-space.ts:23
type PointerFrame struct {
	ViewportWidth  int
	ViewportHeight int
	Left           float64
	Top            float64
	Width          float64
	Height         float64
}

// PointerSpace maps pointer helper coordinates (`clickAt`, `mouseMove`, `drag`
// point targets) from the latest shown viewport screenshot back to viewport CSS
// pixels, unless the caller explicitly sets `css: true`.
// upstream: PointerSpace in pointer-space.ts:45
type PointerSpace struct {
	frame   *PointerFrame
	resized *Viewport
}

// Use records a new pointer frame (or nil to return to 1:1 CSS pixels).
func (p *PointerSpace) Use(frame *PointerFrame) {
	p.frame = frame
	p.resized = nil
}

// Resized records that the viewport size changed since the last screenshot.
func (p *PointerSpace) Resized(vp Viewport) {
	cp := vp
	p.resized = &cp
}

// ToViewport maps (x, y) from screenshot space to viewport CSS pixels.
// If css is true or no scaled frame is active, returns (x, y) unchanged.
func (p *PointerSpace) ToViewport(x, y float64, css bool, current *Viewport) (float64, float64, error) {
	frame := p.frame
	if frame == nil || css {
		return x, y, nil
	}
	vp := current
	if vp == nil {
		vp = p.resized
	}
	if vp != nil && vp.Width > 0 && vp.Height > 0 {
		if math.Abs(float64(vp.Width-frame.ViewportWidth)) > 1 || math.Abs(float64(vp.Height-frame.ViewportHeight)) > 1 {
			return 0, 0, fmt.Errorf(
				"The viewport is %dx%d now, and the last screenshot shows it at %dx%d: pointer positions are read on that image. "+
					"Take a new screenshot, or pass { css: true } to give viewport CSS pixels.",
				vp.Width, vp.Height, frame.ViewportWidth, frame.ViewportHeight,
			)
		}
	}
	if frame.Width <= 0 || frame.Height <= 0 {
		return x, y, nil
	}
	vx := ((x - frame.Left) * float64(frame.ViewportWidth)) / frame.Width
	vy := ((y - frame.Top) * float64(frame.ViewportHeight)) / frame.Height
	return vx, vy, nil
}

// KnownDevice mirrors Puppeteer's KnownDevices profiles used by tab.emulate({ device })
// and tab.devices().
// upstream: emulation.ts:209-227 (Puppeteer KnownDevices)
type KnownDevice struct {
	UserAgent string
	Viewport  Viewport
}

var knownDevices = map[string]KnownDevice{
	"BlackBerry Z30":              {UserAgent: "Mozilla/5.0 (BB10; Touch) AppleWebKit/537.10+ (KHTML, like Gecko) Version/10.0.9.2372 Mobile Safari/537.10+", Viewport: Viewport{Width: 360, Height: 640, DeviceScaleFactor: 2, IsMobile: true, HasTouch: true}},
	"BlackBerry Z30 landscape":    {UserAgent: "Mozilla/5.0 (BB10; Touch) AppleWebKit/537.10+ (KHTML, like Gecko) Version/10.0.9.2372 Mobile Safari/537.10+", Viewport: Viewport{Width: 640, Height: 360, DeviceScaleFactor: 2, IsMobile: true, HasTouch: true}},
	"Galaxy Note 3":               {UserAgent: "Mozilla/5.0 (Linux; U; Android 4.3; en-us; SM-N900T Build/JSS15J) AppleWebKit/534.30 (KHTML, like Gecko) Version/4.0 Mobile Safari/534.30", Viewport: Viewport{Width: 360, Height: 640, DeviceScaleFactor: 3, IsMobile: true, HasTouch: true}},
	"Galaxy Note 3 landscape":     {UserAgent: "Mozilla/5.0 (Linux; U; Android 4.3; en-us; SM-N900T Build/JSS15J) AppleWebKit/534.30 (KHTML, like Gecko) Version/4.0 Mobile Safari/534.30", Viewport: Viewport{Width: 640, Height: 360, DeviceScaleFactor: 3, IsMobile: true, HasTouch: true}},
	"Galaxy S5":                   {UserAgent: "Mozilla/5.0 (Linux; Android 5.0; SM-G900P Build/LRX21T) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/75.0.3765.0 Mobile Safari/537.36", Viewport: Viewport{Width: 360, Height: 640, DeviceScaleFactor: 3, IsMobile: true, HasTouch: true}},
	"Galaxy S5 landscape":         {UserAgent: "Mozilla/5.0 (Linux; Android 5.0; SM-G900P Build/LRX21T) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/75.0.3765.0 Mobile Safari/537.36", Viewport: Viewport{Width: 640, Height: 360, DeviceScaleFactor: 3, IsMobile: true, HasTouch: true}},
	"Galaxy S8":                   {UserAgent: "Mozilla/5.0 (Linux; Android 7.0; SM-G950U Build/NRD90M) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/62.0.3202.84 Mobile Safari/537.36", Viewport: Viewport{Width: 360, Height: 740, DeviceScaleFactor: 3, IsMobile: true, HasTouch: true}},
	"Galaxy S8 landscape":         {UserAgent: "Mozilla/5.0 (Linux; Android 7.0; SM-G950U Build/NRD90M) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/62.0.3202.84 Mobile Safari/537.36", Viewport: Viewport{Width: 740, Height: 360, DeviceScaleFactor: 3, IsMobile: true, HasTouch: true}},
	"Galaxy S9+":                  {UserAgent: "Mozilla/5.0 (Linux; Android 8.0.0; SM-G965U Build/R16NW) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/63.0.3239.111 Mobile Safari/537.36", Viewport: Viewport{Width: 320, Height: 658, DeviceScaleFactor: 4.5, IsMobile: true, HasTouch: true}},
	"Galaxy S9+ landscape":        {UserAgent: "Mozilla/5.0 (Linux; Android 8.0.0; SM-G965U Build/R16NW) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/63.0.3239.111 Mobile Safari/537.36", Viewport: Viewport{Width: 658, Height: 320, DeviceScaleFactor: 4.5, IsMobile: true, HasTouch: true}},
	"Galaxy Tab S4":               {UserAgent: "Mozilla/5.0 (Linux; Android 8.1.0; SM-T837A) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/70.0.3538.80 Safari/537.36", Viewport: Viewport{Width: 712, Height: 1138, DeviceScaleFactor: 2.25, IsMobile: true, HasTouch: true}},
	"Galaxy Tab S4 landscape":     {UserAgent: "Mozilla/5.0 (Linux; Android 8.1.0; SM-T837A) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/70.0.3538.80 Safari/537.36", Viewport: Viewport{Width: 1138, Height: 712, DeviceScaleFactor: 2.25, IsMobile: true, HasTouch: true}},
	"iPad":                        {UserAgent: "Mozilla/5.0 (iPad; CPU OS 11_0 like Mac OS X) AppleWebKit/604.1.34 (KHTML, like Gecko) Version/11.0 Mobile/15A5341f Safari/604.1", Viewport: Viewport{Width: 768, Height: 1024, DeviceScaleFactor: 2, IsMobile: true, HasTouch: true}},
	"iPad landscape":              {UserAgent: "Mozilla/5.0 (iPad; CPU OS 11_0 like Mac OS X) AppleWebKit/604.1.34 (KHTML, like Gecko) Version/11.0 Mobile/15A5341f Safari/604.1", Viewport: Viewport{Width: 1024, Height: 768, DeviceScaleFactor: 2, IsMobile: true, HasTouch: true}},
	"iPad Mini":                   {UserAgent: "Mozilla/5.0 (iPad; CPU OS 11_0 like Mac OS X) AppleWebKit/604.1.34 (KHTML, like Gecko) Version/11.0 Mobile/15A5341f Safari/604.1", Viewport: Viewport{Width: 768, Height: 1024, DeviceScaleFactor: 2, IsMobile: true, HasTouch: true}},
	"iPad Mini landscape":         {UserAgent: "Mozilla/5.0 (iPad; CPU OS 11_0 like Mac OS X) AppleWebKit/604.1.34 (KHTML, like Gecko) Version/11.0 Mobile/15A5341f Safari/604.1", Viewport: Viewport{Width: 1024, Height: 768, DeviceScaleFactor: 2, IsMobile: true, HasTouch: true}},
	"iPad Pro":                    {UserAgent: "Mozilla/5.0 (iPad; CPU OS 11_0 like Mac OS X) AppleWebKit/604.1.34 (KHTML, like Gecko) Version/11.0 Mobile/15A5341f Safari/604.1", Viewport: Viewport{Width: 1024, Height: 1366, DeviceScaleFactor: 2, IsMobile: true, HasTouch: true}},
	"iPad Pro landscape":          {UserAgent: "Mozilla/5.0 (iPad; CPU OS 11_0 like Mac OS X) AppleWebKit/604.1.34 (KHTML, like Gecko) Version/11.0 Mobile/15A5341f Safari/604.1", Viewport: Viewport{Width: 1366, Height: 1024, DeviceScaleFactor: 2, IsMobile: true, HasTouch: true}},
	"iPhone SE":                   {UserAgent: "Mozilla/5.0 (iPhone; CPU iPhone OS 10_3_1 like Mac OS X) AppleWebKit/603.1.30 (KHTML, like Gecko) Version/10.0 Mobile/14E304 Safari/602.1", Viewport: Viewport{Width: 320, Height: 568, DeviceScaleFactor: 2, IsMobile: true, HasTouch: true}},
	"iPhone SE landscape":         {UserAgent: "Mozilla/5.0 (iPhone; CPU iPhone OS 10_3_1 like Mac OS X) AppleWebKit/603.1.30 (KHTML, like Gecko) Version/10.0 Mobile/14E304 Safari/602.1", Viewport: Viewport{Width: 568, Height: 320, DeviceScaleFactor: 2, IsMobile: true, HasTouch: true}},
	"iPhone X":                    {UserAgent: "Mozilla/5.0 (iPhone; CPU iPhone OS 11_0 like Mac OS X) AppleWebKit/604.1.38 (KHTML, like Gecko) Version/11.0 Mobile/15A372 Safari/604.1", Viewport: Viewport{Width: 375, Height: 812, DeviceScaleFactor: 3, IsMobile: true, HasTouch: true}},
	"iPhone X landscape":          {UserAgent: "Mozilla/5.0 (iPhone; CPU iPhone OS 11_0 like Mac OS X) AppleWebKit/604.1.38 (KHTML, like Gecko) Version/11.0 Mobile/15A372 Safari/604.1", Viewport: Viewport{Width: 812, Height: 375, DeviceScaleFactor: 3, IsMobile: true, HasTouch: true}},
	"iPhone XR":                   {UserAgent: "Mozilla/5.0 (iPhone; CPU iPhone OS 12_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/12.0 Mobile/15E148 Safari/604.1", Viewport: Viewport{Width: 414, Height: 896, DeviceScaleFactor: 2, IsMobile: true, HasTouch: true}},
	"iPhone XR landscape":         {UserAgent: "Mozilla/5.0 (iPhone; CPU iPhone OS 12_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/12.0 Mobile/15E148 Safari/604.1", Viewport: Viewport{Width: 896, Height: 414, DeviceScaleFactor: 2, IsMobile: true, HasTouch: true}},
	"iPhone 11":                   {UserAgent: "Mozilla/5.0 (iPhone; CPU iPhone OS 13_2_3 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/13.0.3 Mobile/15E148 Safari/604.1", Viewport: Viewport{Width: 414, Height: 896, DeviceScaleFactor: 2, IsMobile: true, HasTouch: true}},
	"iPhone 11 landscape":         {UserAgent: "Mozilla/5.0 (iPhone; CPU iPhone OS 13_2_3 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/13.0.3 Mobile/15E148 Safari/604.1", Viewport: Viewport{Width: 896, Height: 414, DeviceScaleFactor: 2, IsMobile: true, HasTouch: true}},
	"iPhone 11 Pro":               {UserAgent: "Mozilla/5.0 (iPhone; CPU iPhone OS 13_2_3 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/13.0.3 Mobile/15E148 Safari/604.1", Viewport: Viewport{Width: 375, Height: 812, DeviceScaleFactor: 3, IsMobile: true, HasTouch: true}},
	"iPhone 11 Pro landscape":     {UserAgent: "Mozilla/5.0 (iPhone; CPU iPhone OS 13_2_3 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/13.0.3 Mobile/15E148 Safari/604.1", Viewport: Viewport{Width: 812, Height: 375, DeviceScaleFactor: 3, IsMobile: true, HasTouch: true}},
	"iPhone 12":                   {UserAgent: "Mozilla/5.0 (iPhone; CPU iPhone OS 14_4 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/14.0.3 Mobile/15E148 Safari/604.1", Viewport: Viewport{Width: 390, Height: 844, DeviceScaleFactor: 3, IsMobile: true, HasTouch: true}},
	"iPhone 12 landscape":         {UserAgent: "Mozilla/5.0 (iPhone; CPU iPhone OS 14_4 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/14.0.3 Mobile/15E148 Safari/604.1", Viewport: Viewport{Width: 844, Height: 390, DeviceScaleFactor: 3, IsMobile: true, HasTouch: true}},
	"iPhone 12 Pro":               {UserAgent: "Mozilla/5.0 (iPhone; CPU iPhone OS 14_4 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/14.0.3 Mobile/15E148 Safari/604.1", Viewport: Viewport{Width: 390, Height: 844, DeviceScaleFactor: 3, IsMobile: true, HasTouch: true}},
	"iPhone 12 Pro landscape":     {UserAgent: "Mozilla/5.0 (iPhone; CPU iPhone OS 14_4 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/14.0.3 Mobile/15E148 Safari/604.1", Viewport: Viewport{Width: 844, Height: 390, DeviceScaleFactor: 3, IsMobile: true, HasTouch: true}},
	"iPhone 13":                   {UserAgent: "Mozilla/5.0 (iPhone; CPU iPhone OS 15_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/15.0 Mobile/15E148 Safari/604.1", Viewport: Viewport{Width: 390, Height: 844, DeviceScaleFactor: 3, IsMobile: true, HasTouch: true}},
	"iPhone 13 landscape":         {UserAgent: "Mozilla/5.0 (iPhone; CPU iPhone OS 15_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/15.0 Mobile/15E148 Safari/604.1", Viewport: Viewport{Width: 844, Height: 390, DeviceScaleFactor: 3, IsMobile: true, HasTouch: true}},
	"iPhone 13 Pro":               {UserAgent: "Mozilla/5.0 (iPhone; CPU iPhone OS 15_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/15.0 Mobile/15E148 Safari/604.1", Viewport: Viewport{Width: 390, Height: 844, DeviceScaleFactor: 3, IsMobile: true, HasTouch: true}},
	"iPhone 13 Pro landscape":     {UserAgent: "Mozilla/5.0 (iPhone; CPU iPhone OS 15_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/15.0 Mobile/15E148 Safari/604.1", Viewport: Viewport{Width: 844, Height: 390, DeviceScaleFactor: 3, IsMobile: true, HasTouch: true}},
	"iPhone 13 Pro Max":           {UserAgent: "Mozilla/5.0 (iPhone; CPU iPhone OS 15_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/15.0 Mobile/15E148 Safari/604.1", Viewport: Viewport{Width: 428, Height: 926, DeviceScaleFactor: 3, IsMobile: true, HasTouch: true}},
	"iPhone 13 Pro Max landscape": {UserAgent: "Mozilla/5.0 (iPhone; CPU iPhone OS 15_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/15.0 Mobile/15E148 Safari/604.1", Viewport: Viewport{Width: 926, Height: 428, DeviceScaleFactor: 3, IsMobile: true, HasTouch: true}},
	"iPhone 14":                   {UserAgent: "Mozilla/5.0 (iPhone; CPU iPhone OS 16_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/16.0 Mobile/15E148 Safari/604.1", Viewport: Viewport{Width: 390, Height: 844, DeviceScaleFactor: 3, IsMobile: true, HasTouch: true}},
	"iPhone 14 landscape":         {UserAgent: "Mozilla/5.0 (iPhone; CPU iPhone OS 16_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/16.0 Mobile/15E148 Safari/604.1", Viewport: Viewport{Width: 844, Height: 390, DeviceScaleFactor: 3, IsMobile: true, HasTouch: true}},
	"iPhone 14 Pro":               {UserAgent: "Mozilla/5.0 (iPhone; CPU iPhone OS 16_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/16.0 Mobile/15E148 Safari/604.1", Viewport: Viewport{Width: 393, Height: 852, DeviceScaleFactor: 3, IsMobile: true, HasTouch: true}},
	"iPhone 14 Pro landscape":     {UserAgent: "Mozilla/5.0 (iPhone; CPU iPhone OS 16_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/16.0 Mobile/15E148 Safari/604.1", Viewport: Viewport{Width: 852, Height: 393, DeviceScaleFactor: 3, IsMobile: true, HasTouch: true}},
	"iPhone 15":                   {UserAgent: "Mozilla/5.0 (iPhone; CPU iPhone OS 17_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.0 Mobile/15E148 Safari/604.1", Viewport: Viewport{Width: 393, Height: 852, DeviceScaleFactor: 3, IsMobile: true, HasTouch: true}},
	"iPhone 15 landscape":         {UserAgent: "Mozilla/5.0 (iPhone; CPU iPhone OS 17_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.0 Mobile/15E148 Safari/604.1", Viewport: Viewport{Width: 852, Height: 393, DeviceScaleFactor: 3, IsMobile: true, HasTouch: true}},
	"iPhone 15 Pro":               {UserAgent: "Mozilla/5.0 (iPhone; CPU iPhone OS 17_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.0 Mobile/15E148 Safari/604.1", Viewport: Viewport{Width: 393, Height: 852, DeviceScaleFactor: 3, IsMobile: true, HasTouch: true}},
	"iPhone 15 Pro landscape":     {UserAgent: "Mozilla/5.0 (iPhone; CPU iPhone OS 17_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.0 Mobile/15E148 Safari/604.1", Viewport: Viewport{Width: 852, Height: 393, DeviceScaleFactor: 3, IsMobile: true, HasTouch: true}},
	"Pixel 2":                     {UserAgent: "Mozilla/5.0 (Linux; Android 8.0; Pixel 2 Build/OPD3.170816.012) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/75.0.3765.0 Mobile Safari/537.36", Viewport: Viewport{Width: 411, Height: 731, DeviceScaleFactor: 2.625, IsMobile: true, HasTouch: true}},
	"Pixel 2 landscape":           {UserAgent: "Mozilla/5.0 (Linux; Android 8.0; Pixel 2 Build/OPD3.170816.012) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/75.0.3765.0 Mobile Safari/537.36", Viewport: Viewport{Width: 731, Height: 411, DeviceScaleFactor: 2.625, IsMobile: true, HasTouch: true}},
	"Pixel 5":                     {UserAgent: "Mozilla/5.0 (Linux; Android 11; Pixel 5) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/90.0.4430.91 Mobile Safari/537.36", Viewport: Viewport{Width: 393, Height: 851, DeviceScaleFactor: 2.75, IsMobile: true, HasTouch: true}},
	"Pixel 5 landscape":           {UserAgent: "Mozilla/5.0 (Linux; Android 11; Pixel 5) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/90.0.4430.91 Mobile Safari/537.36", Viewport: Viewport{Width: 851, Height: 393, DeviceScaleFactor: 2.75, IsMobile: true, HasTouch: true}},
	"Pixel 7":                     {UserAgent: "Mozilla/5.0 (Linux; Android 13; Pixel 7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/116.0.0.0 Mobile Safari/537.36", Viewport: Viewport{Width: 412, Height: 915, DeviceScaleFactor: 2.625, IsMobile: true, HasTouch: true}},
	"Pixel 7 landscape":           {UserAgent: "Mozilla/5.0 (Linux; Android 13; Pixel 7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/116.0.0.0 Mobile Safari/537.36", Viewport: Viewport{Width: 915, Height: 412, DeviceScaleFactor: 2.625, IsMobile: true, HasTouch: true}},
}

// KnownDeviceNames returns the sorted device list for tab.devices().
func KnownDeviceNames() []string {
	names := make([]string, 0, len(knownDevices))
	for k := range knownDevices {
		names = append(names, k)
	}
	// Deterministic ASCII sort matching JS Object.keys().sort().
	for i := 1; i < len(names); i++ {
		for j := i; j > 0 && names[j-1] > names[j]; j-- {
			names[j-1], names[j] = names[j], names[j-1]
		}
	}
	return names
}

// parseScrollDeltas parses `(dx, dy)` or `({x, y})` / `({deltaX, deltaY})`
// matching omp's scrollDeltas in tab-arguments.ts:89-108.
func parseScrollDeltas(label string, args []any) (dx, dy float64, opts map[string]any, err error) {
	if len(args) > 0 {
		if m, ok := args[0].(map[string]any); ok {
			_, hasX := m["x"]
			_, hasY := m["y"]
			_, hasDX := m["deltaX"]
			_, hasDY := m["deltaY"]
			if !hasX && !hasY && !hasDX && !hasDY {
				hint := ""
				if dir, ok := m["direction"].(string); ok {
					if amt, ok := m["amount"].(float64); ok && !math.IsNaN(amt) && !math.IsInf(amt, 0) {
						switch strings.ToLower(dir) {
						case "down":
							hint = fmt.Sprintf(" Did you mean %s(0, %v)?", strings.TrimSuffix(label, "()"), amt)
						case "up":
							hint = fmt.Sprintf(" Did you mean %s(0, %v)?", strings.TrimSuffix(label, "()"), -amt)
						case "right":
							hint = fmt.Sprintf(" Did you mean %s(%v, 0)?", strings.TrimSuffix(label, "()"), amt)
						case "left":
							hint = fmt.Sprintf(" Did you mean %s(%v, 0)?", strings.TrimSuffix(label, "()"), -amt)
						}
					}
				}
				return 0, 0, nil, fmt.Errorf("%s takes pixel deltas as numbers: (dx, dy) or ({x, y}); got %s.%s", label, jsLit(m), hint)
			}
			var rx, ry any = 0.0, 0.0
			if hasDX {
				rx = m["deltaX"]
			} else if hasX {
				rx = m["x"]
			}
			if hasDY {
				ry = m["deltaY"]
			} else if hasY {
				ry = m["y"]
			}
			x, okX := rx.(float64)
			y, okY := ry.(float64)
			if !okX || !okY || math.IsNaN(x) || math.IsNaN(y) || math.IsInf(x, 0) || math.IsInf(y, 0) {
				return 0, 0, nil, fmt.Errorf("%s takes pixel deltas as numbers: (dx, dy) or ({x, y}); got (%v, %v)", label, rx, ry)
			}
			return x, y, argMap(args, 1), nil
		}
	}
	var x, y float64
	if len(args) > 0 && args[0] != nil {
		v, ok := args[0].(float64)
		if !ok || math.IsNaN(v) || math.IsInf(v, 0) {
			return 0, 0, nil, fmt.Errorf("%s takes pixel deltas as numbers: (dx, dy) or ({x, y}); got (%v, %v)", label, args[0], argAt(args, 1))
		}
		x = v
	}
	if len(args) > 1 && args[1] != nil {
		v, ok := args[1].(float64)
		if !ok || math.IsNaN(v) || math.IsInf(v, 0) {
			return 0, 0, nil, fmt.Errorf("%s takes pixel deltas as numbers: (dx, dy) or ({x, y}); got (%v, %v)", label, argAt(args, 0), args[1])
		}
		y = v
	}
	return x, y, argMap(args, 2), nil
}

func argAt(args []any, i int) any {
	if i < len(args) {
		return args[i]
	}
	return nil
}

// selectorWaitOpts holds validated waitForSelector options.
// upstream: selectorWaitOptions in selector-wait.ts:18-47
type selectorWaitOpts struct {
	Timeout    *float64
	HasTimeout bool
	State      string // "", "attached", "detached", "visible", "hidden"
	Visible    bool
	Hidden     bool
}

const selectorWaitForms = `waitForSelector() accepts { timeout?: non-negative number (0/Infinity use the run budget), state?: "attached" | "detached" | "visible" | "hidden", visible?: boolean, hidden?: boolean }`

func parseSelectorWaitOptions(raw any) (selectorWaitOpts, error) {
	if raw == nil {
		return selectorWaitOpts{}, nil
	}
	m, ok := raw.(map[string]any)
	if !ok {
		return selectorWaitOpts{}, fmt.Errorf("%s", selectorWaitForms)
	}
	var unknown []string
	for k := range m {
		switch k {
		case "timeout", "state", "visible", "hidden":
		default:
			unknown = append(unknown, k)
		}
	}
	if len(unknown) > 0 {
		return selectorWaitOpts{}, fmt.Errorf("waitForSelector(): unknown option(s): %s. %s", strings.Join(unknown, ", "), selectorWaitForms)
	}
	var out selectorWaitOpts
	if rawT, exists := m["timeout"]; exists && rawT != nil {
		v, ok := rawT.(float64)
		if !ok || math.IsNaN(v) || v < 0 {
			return selectorWaitOpts{}, fmt.Errorf("%s", selectorWaitForms)
		}
		out.Timeout = &v
		out.HasTimeout = true
	}
	if rawS, exists := m["state"]; exists && rawS != nil {
		s, ok := rawS.(string)
		if !ok || (s != "attached" && s != "detached" && s != "visible" && s != "hidden") {
			return selectorWaitOpts{}, fmt.Errorf("%s", selectorWaitForms)
		}
		out.State = s
	}
	var hasVis, hasHid bool
	if rawV, exists := m["visible"]; exists && rawV != nil {
		b, ok := rawV.(bool)
		if !ok {
			return selectorWaitOpts{}, fmt.Errorf("%s", selectorWaitForms)
		}
		out.Visible = b
		hasVis = true
	}
	if rawH, exists := m["hidden"]; exists && rawH != nil {
		b, ok := rawH.(bool)
		if !ok {
			return selectorWaitOpts{}, fmt.Errorf("%s", selectorWaitForms)
		}
		out.Hidden = b
		hasHid = true
	}
	if out.Visible && out.Hidden {
		return selectorWaitOpts{}, fmt.Errorf("waitForSelector(): visible and hidden cannot both be true. %s", selectorWaitForms)
	}
	if out.State != "" {
		vis := out.State == "visible"
		hid := out.State == "hidden"
		if (hasVis && out.Visible != vis) || (hasHid && out.Hidden != hid) {
			return selectorWaitOpts{}, fmt.Errorf("waitForSelector(): state conflicts with visible/hidden. %s", selectorWaitForms)
		}
		out.Visible = vis
		out.Hidden = hid
	}
	return out, nil
}

// validateScreenshotOpts validates screenshot options and rejects unsupported keys/options.
// upstream: validateScreenshotOptions + screenshotQuality in screenshot.ts:60-74, 210-218
func validateScreenshotOpts(opts map[string]any) error {
	if opts == nil {
		return nil
	}
	allowed := map[string]bool{
		"selector": true, "fullPage": true, "silent": true, "annotate": true,
		"format": true, "quality": true, "ifChanged": true, "threshold": true, "path": true,
	}
	for k := range opts {
		if !allowed[k] {
			return fmt.Errorf(`tab.screenshot() has no %q option; it takes selector, fullPage, silent, annotate, format, quality, ifChanged, threshold, path`, k)
		}
	}
	if f, ok := opts["format"]; ok && f != nil {
		fs, ok := f.(string)
		if !ok || (fs != "png" && fs != "jpeg" && fs != "webp") {
			return fmt.Errorf("tab.screenshot() format must be one of png, jpeg, webp")
		}
	}
	if p, ok := opts["path"]; ok && p != nil {
		ps, ok := p.(string)
		if !ok || ps == "" {
			return fmt.Errorf("tab.screenshot() path must be a non-empty file path")
		}
	}
	if q, ok := opts["quality"]; ok && q != nil {
		f, _ := opts["format"].(string)
		if f != "jpeg" && f != "webp" {
			return fmt.Errorf(`Screenshot quality is only valid with format: "jpeg" or "webp"`)
		}
		qf, ok := q.(float64)
		if !ok || math.IsNaN(qf) || qf < 0 || qf > 100 || qf != math.Trunc(qf) {
			return fmt.Errorf("Screenshot quality must be an integer from 0 to 100")
		}
	}
	if a, ok := opts["annotate"].(bool); ok && a {
		return fmt.Errorf("tab.screenshot({ annotate: true }) is not yet ported on the Go CDP backend (see internal/browser/UPSTREAM.md)")
	}
	if ic, ok := opts["ifChanged"].(bool); ok && ic {
		return fmt.Errorf("tab.screenshot({ ifChanged: true }) is not yet ported on the Go CDP backend (see internal/browser/UPSTREAM.md)")
	}
	if _, hasThresh := opts["threshold"]; hasThresh && opts["threshold"] != nil {
		return fmt.Errorf("tab.screenshot({ threshold }) is not yet ported on the Go CDP backend (see internal/browser/UPSTREAM.md)")
	}
	return nil
}

// formatSelectorMatchHint matches omp's formatSelectorMatchHint in tab-worker.ts:1897.
func formatSelectorMatchHint(count int) string {
	if count == 0 {
		return "; selector currently matches no elements — run tab.observe() or tab.ariaSnapshot() to inspect the page"
	}
	return fmt.Sprintf("; selector currently matches %d element(s) but the action never became possible — the element may be hidden or covered (try tab.scrollIntoView() or a more specific selector)", count)
}
