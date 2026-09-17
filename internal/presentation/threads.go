package presentation

import (
	"runtime"
	"sync"

	"github.com/This-Is-NPC/backstage/internal/scene"
)

// Test-only seams. observeRender is nil in production; noteRender calls it under observeMu.
var (
	prepareSerial        = false
	screenshotOptimize   = true
	ffv1GOP              = 1
	screenshotObserver   func(optimize bool)
	observeRender        func(renderNote)
	observeFrame         func(n int, t float64, png []byte)
	observeComposedFrame func(n int, t float64, png []byte)
	layerMode            = "auto"
	observeMu            sync.Mutex
	prepareOne           = prepareTrack
)

type renderNote struct {
	PrepareArgs   []string
	PreparedPaths map[string]string
	EncoderArgs   []string
	MuxArgs       []string
	ConcatArgs    []string
	EncodeThreads int
}

func noteRender(note renderNote) {
	observeMu.Lock()
	defer observeMu.Unlock()
	if observeRender != nil {
		observeRender(note)
	}
}

func planThreadCfg(p *Plan) scene.RenderThreads {
	if p == nil || p.Project == nil {
		return scene.RenderThreads{}
	}
	return p.Project.Render.Threads
}

func resolveRenderThreads(cfg scene.RenderThreads, tracks int) (prepare, filter, encode int) {
	n := runtime.NumCPU()
	if n < 1 {
		n = 1
	}
	w := tracks
	if w < 1 {
		w = 1
	}
	if w > n {
		w = n
	}
	prepare = cfg.Prepare
	if prepare == 0 {
		prepare = n / w
		if prepare < 1 {
			prepare = 1
		}
	}
	filter = cfg.Filter
	if filter == 0 {
		filter = 1
	}
	encode = cfg.Encode
	if encode == 0 {
		encode = n
	}
	return
}
