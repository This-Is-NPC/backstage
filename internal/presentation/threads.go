package presentation

import (
	"sync"

	"github.com/This-Is-NPC/backstage/internal/scene"
)

// Test-only seams. observeRender is nil in production; noteRender calls it under observeMu.
var (
	screenshotOptimize   = true
	screenshotObserver   func(optimize bool)
	observeRender        func(renderNote)
	observeFrame         func(n int, t float64, png []byte)
	observeComposedFrame func(n int, t float64, png []byte)
	layerMode            = "auto"
	observeMu            sync.Mutex
	prepareOne           = prepareTrack
)

type renderNote struct {
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
