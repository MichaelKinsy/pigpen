package websearch

import (
	"math"
)

// Port of defuddle 0.19.3 dist/extractors/bilibili.js, synchronous surface only. extract() is buildResult() without
// the data of the view API: the player iframe for the video of the URL, with the page's title. Fetching the view
// data, the subtitle tracks and the subtitle file (extractAsync and everything below it) is reached only through
// extractAsync and is not ported.

type dfdBilibili struct {
	ctx      dfdExtCtx
	bvid     string
	haveBvid bool
}

var reBilibiliBvid = jsRE(`\/video\/(BV[0-9A-Za-z]+)\/?`, "")

func init() {
	dfdRegisterExtractor("bilibili", func(c dfdExtCtx) dfdExtractor { return &dfdBilibili{ctx: c} })
}

func (e *dfdBilibili) name() string { return "bilibili" }

func (e *dfdBilibili) canExtract() bool { return e.getBvid() != "" }

func (e *dfdBilibili) getBvid() string {
	if !e.haveBvid {
		e.haveBvid = true
		if u, ok := newURL(e.ctx.url, ""); ok {
			if m := reBilibiliBvid.FindStringSubmatch(u.pathname()); m != nil {
				e.bvid = m[1]
			}
		}
	}
	return e.bvid
}

func (e *dfdBilibili) getPageNumber() float64 {
	u, ok := newURL(e.ctx.url, "")
	if !ok {
		return 1
	}
	p, _ := extQueryParam(u.search(), "p")
	n := jsParseInt(orDefault(p, "1"))
	if n > 0 && !math.IsInf(n, 0) {
		return n
	}
	return 1
}

func (e *dfdBilibili) extract() dfdExtracted {
	bvid := e.getBvid()
	title := e.ctx.doc.title()
	contentHTML := ""
	if bvid != "" {
		// bvid is letters and digits only, which encodeURIComponent leaves alone.
		src := "https://player.bilibili.com/player.html?bvid=" + bvid + "&page=" + jsNumberString(e.getPageNumber()) + "&high_quality=1&danmaku=0"
		contentHTML += `<iframe width="560" height="315" src="` + src + `" title="Bilibili video player" frameborder="0" allowfullscreen></iframe>`
	}
	return dfdExtracted{
		content:     contentHTML,
		contentHTML: contentHTML,
		variables:   map[string]string{"title": title, "site": "Bilibili"},
	}
}
