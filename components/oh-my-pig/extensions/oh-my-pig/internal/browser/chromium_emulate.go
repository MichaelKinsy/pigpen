package browser

import (
	"context"
	"encoding/base64"
	"fmt"
)

// --- Emulation controller (upstream: emulation.ts) ---

var allowedEmulateKeys = map[string]bool{
	"viewport": true, "device": true, "geolocation": true, "offline": true,
	"colorScheme": true, "reducedMotion": true, "media": true, "headers": true,
	"credentials": true, "userAgent": true, "timezone": true, "locale": true,
	"cpuThrottling": true, "network": true,
}

func (t *cdpTab) emulate(ctx context.Context, opts map[string]any) (*CallResult, error) {
	if opts != nil {
		for k := range opts {
			if !allowedEmulateKeys[k] {
				return nil, fmt.Errorf(
					"tab.emulate() received invalid option %q. It takes viewport ({width, height, scale?, isMobile?, hasTouch?}), device, geolocation, offline, colorScheme, reducedMotion, media, headers, credentials, userAgent, timezone, locale, cpuThrottling, network.",
					k,
				)
			}
		}
	}

	t.mu.Lock()
	if t.emulateState == nil {
		t.emulateState = map[string]any{}
	}
	for k, v := range opts {
		if k == "reducedMotion" {
			switch rv := v.(type) {
			case bool:
				t.emulateState[k] = rv
			case string:
				if rv != "reduce" && rv != "no-preference" {
					t.mu.Unlock()
					return nil, fmt.Errorf("tab.emulate() invalid reducedMotion %q", rv)
				}
				t.emulateState[k] = rv == "reduce"
			case nil:
				t.emulateState[k] = nil
			default:
				t.mu.Unlock()
				return nil, fmt.Errorf("tab.emulate() invalid reducedMotion")
			}
			continue
		}
		t.emulateState[k] = v
	}
	stateCopy := make(map[string]any, len(t.emulateState))
	for k, v := range t.emulateState {
		stateCopy[k] = v
	}
	t.mu.Unlock()

	if devName, ok := stateCopy["device"].(string); ok && devName != "" {
		dev, exists := knownDevices[devName]
		if !exists {
			return nil, fmt.Errorf("Unknown Puppeteer device %q. Use tab.devices().", devName)
		}
		if err := t.applyViewport(ctx, dev.Viewport); err != nil {
			return nil, err
		}
		_ = t.send(ctx, "Network.setUserAgentOverride", map[string]any{"userAgent": dev.UserAgent}, nil)
	}

	if vpRaw, ok := stateCopy["viewport"].(map[string]any); ok {
		vp := t.Info().Viewport
		if w, ok := vpRaw["width"].(float64); ok {
			vp.Width = int(w)
		}
		if h, ok := vpRaw["height"].(float64); ok {
			vp.Height = int(h)
		}
		if s, ok := vpRaw["scale"].(float64); ok {
			vp.DeviceScaleFactor = s
		} else if s, ok := vpRaw["deviceScaleFactor"].(float64); ok {
			vp.DeviceScaleFactor = s
		}
		vp.IsMobile, _ = vpRaw["isMobile"].(bool)
		vp.HasTouch, _ = vpRaw["hasTouch"].(bool)
		if err := t.applyViewport(ctx, vp); err != nil {
			return nil, err
		}
	}

	if uaRaw, hasUA := stateCopy["userAgent"]; hasUA {
		if uaRaw == nil {
			restoreUA := t.baseUserAgent
			if devName, ok := stateCopy["device"].(string); ok {
				if dev, exists := knownDevices[devName]; exists {
					restoreUA = dev.UserAgent
				}
			}
			_ = t.send(ctx, "Network.setUserAgentOverride", map[string]any{"userAgent": restoreUA}, nil)
		} else if ua, ok := uaRaw.(string); ok {
			if err := t.send(ctx, "Network.setUserAgentOverride", map[string]any{"userAgent": ua}, nil); err != nil {
				return nil, err
			}
		}
	}

	// Media features (colorScheme + reducedMotion + media)
	var features []map[string]string
	if cs, ok := stateCopy["colorScheme"].(string); ok {
		features = append(features, map[string]string{"name": "prefers-color-scheme", "value": cs})
	}
	if rm, ok := stateCopy["reducedMotion"].(bool); ok {
		val := "no-preference"
		if rm {
			val = "reduce"
		}
		features = append(features, map[string]string{"name": "prefers-reduced-motion", "value": val})
	}
	mediaParams := map[string]any{}
	if len(features) > 0 {
		mediaParams["features"] = features
	}
	if medRaw, hasMed := stateCopy["media"]; hasMed {
		if medRaw == nil {
			mediaParams["media"] = ""
		} else if med, ok := medRaw.(string); ok {
			mediaParams["media"] = med
		}
	}
	if len(mediaParams) > 0 {
		_ = t.send(ctx, "Emulation.setEmulatedMedia", mediaParams, nil)
	}

	// Offline / network conditions
	_, hasOffline := stateCopy["offline"]
	_, hasNet := stateCopy["network"]
	if hasOffline || hasNet {
		offline, _ := stateCopy["offline"].(bool)
		latency := 0.0
		down := -1.0
		up := -1.0
		switch nv := stateCopy["network"].(type) {
		case string:
			switch nv {
			case "slow3g":
				down, up, latency = 500*1024/8, 500*1024/8, 400
			case "fast3g":
				down, up, latency = 1.6*1024*1024/8, 750*1024/8, 150
			default:
				return nil, fmt.Errorf("tab.emulate() unknown network preset %q (use slow3g, fast3g, or {download, upload, latency})", nv)
			}
		case map[string]any:
			down, _ = nv["download"].(float64)
			up, _ = nv["upload"].(float64)
			latency, _ = nv["latency"].(float64)
		}
		_ = t.send(ctx, "Network.emulateNetworkConditions", map[string]any{
			"offline":            offline,
			"latency":            latency,
			"downloadThroughput": down,
			"uploadThroughput":   up,
		}, nil)
	}

	if tzRaw, hasTZ := stateCopy["timezone"]; hasTZ {
		tz := ""
		if s, ok := tzRaw.(string); ok {
			tz = s
		}
		_ = t.send(ctx, "Emulation.setTimezoneOverride", map[string]any{"timezoneId": tz}, nil)
	}
	if locRaw, hasLoc := stateCopy["locale"]; hasLoc {
		loc := ""
		if s, ok := locRaw.(string); ok {
			loc = s
		}
		_ = t.send(ctx, "Emulation.setLocaleOverride", map[string]any{"locale": loc}, nil)
	}
	if geoRaw, hasGeo := stateCopy["geolocation"]; hasGeo {
		if geoRaw == nil {
			_ = t.send(ctx, "Emulation.clearGeolocationOverride", nil, nil)
		} else if geo, ok := geoRaw.(map[string]any); ok {
			lat, _ := geo["latitude"].(float64)
			lng, _ := geo["longitude"].(float64)
			acc := 10.0
			if a, ok := geo["accuracy"].(float64); ok {
				acc = a
			}
			_ = t.send(ctx, "Emulation.setGeolocationOverride", map[string]any{
				"latitude":  lat,
				"longitude": lng,
				"accuracy":  acc,
			}, nil)
		}
	}
	if cpuRaw, hasCPU := stateCopy["cpuThrottling"]; hasCPU {
		rate := 1.0
		if f, ok := cpuRaw.(float64); ok && f >= 1 {
			rate = f
		}
		_ = t.send(ctx, "Emulation.setCPUThrottlingRate", map[string]any{"rate": rate}, nil)
	}
	_, hasHdr := stateCopy["headers"]
	_, hasCred := stateCopy["credentials"]
	if hasHdr || hasCred {
		headers := map[string]any{}
		if m, ok := stateCopy["headers"].(map[string]any); ok {
			for k, v := range m {
				headers[k] = v
			}
		}
		if cm, ok := stateCopy["credentials"].(map[string]any); ok {
			u, _ := cm["username"].(string)
			p, _ := cm["password"].(string)
			headers["Authorization"] = "Basic " + base64.StdEncoding.EncodeToString([]byte(u+":"+p))
		}
		_ = t.send(ctx, "Network.setExtraHTTPHeaders", map[string]any{"headers": headers}, nil)
	}
	return &CallResult{Value: stateCopy}, nil
}
