package presentation

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

func Render(ctx context.Context, p *Plan, out string, progress io.Writer) error {
	return renderRange(ctx, p, out, progress, fullRenderOpts(p))
}

type firstFail struct {
	once   sync.Once
	idx    int
	err    error
	cancel context.CancelFunc
}

func (f *firstFail) claim(idx int, err error) bool {
	ok := false
	f.once.Do(func() {
		f.idx = idx
		f.err = err
		ok = true
		if f.cancel != nil {
			f.cancel()
		}
	})
	return ok
}

type chunkResult struct {
	index                                      int
	rendered                                   bool
	layered                                    bool
	decode, transfer, draw, shot, write, probe msHist
	decodedPNG, screenshotBytes                int64
	rendererStart, decoderStart                float64
	encode, wall, composite                    float64
	files                                      map[string]string
}

func renderRange(ctx context.Context, p *Plan, out string, progress io.Writer, opts renderOpts) (err error) {
	if opts.Scale == 0 {
		opts.Scale = 1
	}
	if opts.End <= opts.First {
		return fmt.Errorf("interval has no frames")
	}
	started := time.Now()
	if progress == nil {
		progress = io.Discard
	}
	progress = &syncWriter{w: progress}
	browser, err := dependencies()
	if err != nil {
		return err
	}
	if err = os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
		return err
	}
	work, err := os.MkdirTemp(filepath.Dir(out), ".backstage-render-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(work)
	var timings renderTimings
	cache, err := openRenderCache(ctx, progress)
	if err != nil {
		return err
	}
	defer cache.Close()
	browserVer, err := toolVersion(ctx, browser, "--version")
	if err != nil {
		return fmt.Errorf("browser version: %w", err)
	}
	segs := newSegmentCache(cache, p, browserVer)
	trackPaths, prepare, err := prepareTracks(ctx, p, work, progress, cache)
	if err != nil {
		return err
	}
	if len(prepare) > 0 {
		timings.PrepareTrackSeconds = prepare
	}
	fmt.Fprintln(progress, ">> prepare audio")
	mixAt := time.Now()
	audio, parts, err := p.mix(ctx, work, cache)
	if err != nil {
		return err
	}
	if audio != "" {
		sec := roundSec(time.Since(mixAt))
		timings.AudioSeconds = &sec
		if len(parts) > 0 {
			timings.AudioPartSeconds = parts
		}
	}
	nFrames := opts.End - opts.First
	chunks := planChunks(p, opts.First, opts.End)
	if len(chunks) == 0 {
		return fmt.Errorf("interval has no frames")
	}
	nChunks := len(chunks)
	files := map[string]string{}
	results := make([]chunkResult, nChunks)
	var miss []chunkJob
	for _, ch := range chunks {
		key, err := segs.cacheKey(opts, ch)
		if err != nil {
			return err
		}
		job := chunkJob{chunkRange: ch, key: key}
		workPath := chunkVideoPath(work, nChunks, ch.Index)
		hit, err := cache.getOrFill(ctx, "segments", key, ".mp4", workPath, fmt.Sprintf("chunk-%d", ch.Index), segs.entryValid, nil)
		if err != nil {
			return err
		}
		if hit {
			recordSegmentHit(progress, cache, job, segs.manifestAbs(segs.dest(key)), results)
			continue
		}
		miss = append(miss, job)
	}
	_, _, encodeAll := resolveRenderThreads(planThreadCfg(p), len(p.Tracks))
	nWorkers, err := resolveWorkers(planWorkers(p), len(miss))
	if err != nil {
		return err
	}
	timings.Workers = nWorkers
	encodeThreads := chunkEncodeThreads(encodeAll, nWorkers)
	video := filepath.Join(work, "video.mp4")
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	meta := &trackMeta{}
	var completed atomic.Int64
	var fail firstFail
	fail.cancel = cancel
	if nWorkers > 0 {
		slices := splitIndexSlices(len(miss), nWorkers)
		var wg sync.WaitGroup
		for _, sl := range slices {
			part := append([]chunkJob(nil), miss[sl[0]:sl[1]]...)
			wg.Add(1)
			go func(part []chunkJob) {
				defer wg.Done()
				runChunkWorker(ctx, runCtx, p, work, trackPaths, progress, opts, part, nChunks, nFrames, encodeThreads, cache, segs, &completed, &fail, results, meta)
			}(part)
		}
		wg.Wait()
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	if fail.err != nil {
		return fmt.Errorf("chunk %d: %w", fail.idx, fail.err)
	}
	if nChunks > 1 {
		concatAt := time.Now()
		if err = concatChunks(ctx, work, chunks, video); err != nil {
			return err
		}
		sec := roundSec(time.Since(concatAt))
		timings.ConcatSeconds = &sec
	}
	for rel, path := range mergeChunkResults(results, &timings) {
		if path != "" {
			files[rel] = path
		}
	}
	final := video
	if audio != "" {
		final = filepath.Join(work, "final.mp4")
		muxAt := time.Now()
		muxArgs := muxAudioArgs(p, opts, video, audio, final)
		noteRender(renderNote{MuxArgs: append([]string(nil), muxArgs...)})
		if err = run(ctx, "ffmpeg", muxArgs...); err != nil {
			return err
		}
		sec := roundSec(time.Since(muxAt))
		timings.MuxSeconds = &sec
	} else if nChunks == 1 {
		exported := filepath.Join(work, "export.mp4")
		if err = copyRegular(video, exported, 0o644); err != nil {
			return err
		}
		final = exported
	}
	timings.CacheHits = cache.hits
	timings.CacheMisses = cache.misses
	collectWorkBytes(&timings, work, p, trackPaths)
	versions := map[string]string{filepath.Base(browser): browserVer, "ffmpeg": cache.ffmpeg}
	if err = writeRenderFacts(ctx, p, work, files, &timings, started, versions); err != nil {
		return err
	}
	fmt.Fprint(progress, timings.progressLine(nFrames))
	if err = os.Rename(final, out); err != nil {
		return err
	}
	return os.Rename(filepath.Join(work, "facts.json"), strings.TrimSuffix(out, filepath.Ext(out))+".facts.json")
}

func muxAudioArgs(p *Plan, opts renderOpts, video, audio, final string) []string {
	if opts.First == 0 && opts.End == p.Frames {
		return []string{"-v", "error", "-y", "-i", video, "-i", audio, "-map", "0:v:0", "-map", "1:a:0", "-c:v", "copy", "-c:a", "aac", "-b:a", "192k", "-t", num(p.Document.Duration), "-movflags", "+faststart", final}
	}
	ss, es := audioSamples(opts.First, opts.End, p.FPS)
	return []string{"-v", "error", "-y", "-i", video, "-i", audio, "-filter_complex", "[1:a]" + muxAudioFilter(ss, es) + "[a]", "-map", "0:v:0", "-map", "[a]", "-c:v", "copy", "-c:a", "aac", "-b:a", "192k", "-t", num(intervalDuration(opts.First, opts.End, p.FPS)), "-movflags", "+faststart", final}
}

func concatChunks(ctx context.Context, work string, chunks []chunkRange, video string) error {
	list := filepath.Join(work, "concat.txt")
	var b strings.Builder
	for _, ch := range chunks {
		p := filepath.Join(work, fmt.Sprintf("chunk-%d.mp4", ch.Index))
		fmt.Fprintf(&b, "file '%s'\n", strings.ReplaceAll(p, "'", `'\''`))
	}
	if err := os.WriteFile(list, []byte(b.String()), 0o600); err != nil {
		return err
	}
	args := []string{"-v", "error", "-y", "-f", "concat", "-safe", "0", "-i", list, "-c", "copy", video}
	noteRender(renderNote{ConcatArgs: append([]string(nil), args...)})
	return run(ctx, "ffmpeg", args...)
}

func mergeChunkResults(results []chunkResult, timings *renderTimings) map[string]string {
	files := map[string]string{}
	var decodeH, transferH, drawH, shotH, writeH, probeH msHist
	encodeMax := 0.0
	for _, res := range results {
		for rel, path := range res.files {
			files[rel] = path
		}
		if !res.rendered {
			continue
		}
		decodeH.merge(res.decode)
		transferH.merge(res.transfer)
		drawH.merge(res.draw)
		shotH.merge(res.shot)
		writeH.merge(res.write)
		probeH.merge(res.probe)
		timings.DecodedPNGBytes += res.decodedPNG
		timings.ScreenshotBytes += res.screenshotBytes
		timings.RendererStartSeconds += res.rendererStart
		timings.DecoderStartSeconds += res.decoderStart
		timings.CompositeSeconds += res.composite
		if res.layered {
			timings.LayeredChunks++
		} else if res.encode > encodeMax {
			encodeMax = res.encode
		}
		timings.ChunkSeconds = append(timings.ChunkSeconds, namedSeconds{ID: fmt.Sprintf("chunk-%d", res.index), Seconds: res.wall})
	}
	timings.EncodeSeconds = encodeMax
	timings.Decode = decodeH.snapshot()
	timings.Transfer = transferH.snapshot()
	timings.Draw = drawH.snapshot()
	timings.Screenshot = shotH.snapshot()
	timings.EncodeWrite = writeH.snapshot()
	timings.Probe = probeH.snapshot()
	return files
}

func ensureDecoder(ctx context.Context, decoders map[string]*decoder, id, path string, index, fps int) (*decoder, time.Duration, error) {
	d := decoders[id]
	if d != nil && (d.done || index == d.frame || index == d.frame+1) {
		return d, 0, nil
	}
	if d != nil {
		d.close()
	}
	started := time.Now()
	nd, err := openTrackDecoder(ctx, path, index, fps)
	if err != nil {
		delete(decoders, id)
		return nil, 0, err
	}
	decoders[id] = nd
	return nd, time.Since(started), nil
}

func runChunkWorker(parent, ctx context.Context, p *Plan, work string, trackPaths map[string]string, progress io.Writer, opts renderOpts, miss []chunkJob, nChunks, nFrames, encodeThreads int, cache *renderCache, segs *segmentCache, completed *atomic.Int64, fail *firstFail, results []chunkResult, meta *trackMeta) {
	if len(miss) == 0 {
		return
	}
	rendererAt := time.Now()
	r, err := NewRenderer(ctx, p)
	if err != nil {
		status := "failed"
		if parent.Err() != nil {
			status = "interrupted"
		} else if !fail.claim(miss[0].Index, err) {
			status = "interrupted"
		}
		fmt.Fprintf(progress, ">> chunk-%d %s\n", miss[0].Index, status)
		return
	}
	r.scale = opts.Scale
	rendererStart := roundSec(time.Since(rendererAt))
	defer r.Close()
	decoders := map[string]*decoder{}
	defer func() {
		for _, d := range decoders {
			d.close()
		}
	}()
	first := true
	for _, job := range miss {
		ch := job.chunkRange
		workPath := chunkVideoPath(work, nChunks, ch.Index)
		var res chunkResult
		hit, err := cache.getOrFill(ctx, "segments", job.key, ".mp4", workPath, fmt.Sprintf("chunk-%d", ch.Index), segs.entryValid, func(tmp string) (map[string]string, error) {
			out, e := renderChunk(parent, ctx, r, p, work, trackPaths, decoders, progress, opts, ch, nFrames, encodeThreads, completed, fail, tmp, meta)
			res = out
			if e != nil {
				return nil, e
			}
			return segs.hashFiles(res.files)
		})
		if err != nil {
			if parent.Err() != nil {
				return
			}
			if fail.claim(ch.Index, err) {
				fmt.Fprintf(progress, ">> chunk-%d failed\n", ch.Index)
			}
			return
		}
		if hit {
			recordSegmentHit(progress, cache, job, segs.manifestAbs(segs.dest(job.key)), results)
			continue
		}
		cache.note(false, "segments")
		if first {
			res.rendererStart = rendererStart
			first = false
		}
		results[ch.Index] = res
		fmt.Fprintf(progress, ">> chunk-%d ok\n", ch.Index)
	}
}

func renderChunk(parent, ctx context.Context, r *Renderer, p *Plan, work string, trackPaths map[string]string, decoders map[string]*decoder, progress io.Writer, opts renderOpts, ch chunkRange, nFrames, encodeThreads int, completed *atomic.Int64, fail *firstFail, out string, meta *trackMeta) (chunkResult, error) {
	res := chunkResult{index: ch.Index}
	fmt.Fprintf(progress, ">> chunk-%d running\n", ch.Index)
	status := "ok"
	defer func() {
		if status == "ok" {
			return
		}
		if parent.Err() != nil {
			status = "interrupted"
		}
		fmt.Fprintf(progress, ">> chunk-%d %s\n", ch.Index, status)
	}()
	setFail := func(err error) error {
		if parent.Err() != nil {
			status = "interrupted"
			return parent.Err()
		}
		if fail.claim(ch.Index, err) {
			status = "failed"
			return err
		}
		status = "interrupted"
		return err
	}
	started := time.Now()
	for _, id := range sortedKeys(p.Tracks) {
		tr := p.Tracks[id]
		if trackNeeded(tr, ch.First, ch.End, p.FPS) {
			continue
		}
		if d := decoders[id]; d != nil {
			d.close()
			delete(decoders, id)
		}
	}
	mode := currentLayerMode()
	var probes []probeFrame
	if mode != "frames" && !skipLayerProbe(p, opts, ch) {
		for n := ch.First; n < ch.End; n++ {
			if err := ctx.Err(); err != nil {
				return res, setFail(err)
			}
			pr, dur, err := r.probeTimed(float64(n) / float64(p.FPS))
			if err != nil {
				return res, setFail(fmt.Errorf("probe %d: %w", n, err))
			}
			res.probe.add(dur)
			probes = append(probes, pr)
		}
	}
	useLayered := false
	if len(probes) > 0 {
		dec := chunkEligible(p, ch, probes)
		switch mode {
		case "layered":
			if !dec.Layered {
				return res, setFail(fmt.Errorf("layered: %s", dec.Reason))
			}
			useLayered = true
		case "frames":
			useLayered = false
		default:
			useLayered = dec.Layered
		}
	} else if mode == "layered" {
		return res, setFail(fmt.Errorf("layered: not eligible"))
	}
	if useLayered {
		fmt.Fprintf(progress, ">> chunk-%d layered\n", ch.Index)
		if observeChunkDecoders != nil {
			observeChunkDecoders(ch.Index, sortedKeys(decoders))
		}
		layered, err := renderLayeredChunk(ctx, r, p, work, trackPaths, ch, nFrames, encodeThreads, completed, progress, out, &res, probes, meta)
		if err != nil {
			return res, setFail(err)
		}
		layered.wall = roundSec(time.Since(started))
		layered.files = r.filesForEvents(chunkEvents(p, ch)...)
		layered.rendered = true
		layered.layered = true
		return layered, nil
	}
	return renderFramesChunk(ctx, r, p, trackPaths, decoders, progress, opts, ch, nFrames, encodeThreads, completed, setFail, started, out, res)
}

func renderLayeredChunk(ctx context.Context, r *Renderer, p *Plan, work string, trackPaths map[string]string, ch chunkRange, nFrames, encodeThreads int, completed *atomic.Int64, progress io.Writer, out string, res *chunkResult, probes []probeFrame, meta *trackMeta) (chunkResult, error) {
	if failAtFrame != nil {
		for n := ch.First; n < ch.End; n++ {
			if e := failAtFrame(ch.Index, n); e != nil {
				return *res, e
			}
		}
	}
	t := float64(ch.First) / float64(p.FPS)
	below, above, shotBelow, shotAbove, err := r.captureLayers(t)
	res.shot.add(shotBelow)
	res.shot.add(shotAbove)
	if err != nil {
		return *res, err
	}
	res.screenshotBytes += int64(len(below) + len(above))
	boxes := make([]slotBox, len(probes[0].Geometry))
	for i, g := range probes[0].Geometry {
		boxes[i] = snapSlot(g)
	}
	observeMu.Lock()
	frameObs := observeFrame
	compObs := observeComposedFrame
	observeMu.Unlock()
	var observe func(n int, png []byte) error
	if frameObs != nil || compObs != nil {
		observe = func(n int, png []byte) error {
			tt := float64(n) / float64(p.FPS)
			observeMu.Lock()
			if observeFrame != nil {
				observeFrame(n, tt, png)
			}
			if observeComposedFrame != nil {
				observeComposedFrame(n, tt, png)
			}
			observeMu.Unlock()
			return nil
		}
	}
	encodeAt := time.Now()
	err = compositeChunk(ctx, p, ch, trackPaths, boxes, below, above, encodeThreads, work, out, meta, observe)
	res.composite = roundSec(time.Since(encodeAt))
	if err != nil {
		return *res, err
	}
	nOut := ch.End - ch.First
	for i := 0; i < nOut; i++ {
		k := int(completed.Add(1) - 1)
		if k%p.FPS == 0 {
			fmt.Fprintf(progress, ">> render %d/%d frames\n", k, nFrames)
		}
	}
	return *res, nil
}

func renderFramesChunk(ctx context.Context, r *Renderer, p *Plan, trackPaths map[string]string, decoders map[string]*decoder, progress io.Writer, opts renderOpts, ch chunkRange, nFrames, encodeThreads int, completed *atomic.Int64, setFail func(error) error, started time.Time, out string, res chunkResult) (chunkResult, error) {
	var decoderDur time.Duration
	for _, id := range sortedKeys(p.Tracks) {
		tr := p.Tracks[id]
		if !trackNeeded(tr, ch.First, ch.End, p.FPS) {
			continue
		}
		index := trackFrame(tr, ch.First, p.FPS)
		_, extra, e := ensureDecoder(ctx, decoders, id, trackPaths[id], index, p.FPS)
		if e != nil {
			return res, setFail(e)
		}
		decoderDur += extra
	}
	if observeChunkDecoders != nil {
		observeChunkDecoders(ch.Index, sortedKeys(decoders))
	}
	encArgs := encoderArgs(p, opts, encodeThreads, out)
	noteRender(renderNote{EncodeThreads: encodeThreads, EncoderArgs: append([]string(nil), encArgs...)})
	encoder := command(ctx, "ffmpeg", encArgs...)
	var stderr bytes.Buffer
	encoder.Stderr = &stderr
	pipe, err := encoder.StdinPipe()
	if err != nil {
		return res, setFail(err)
	}
	encodeAt := time.Now()
	if err = encoder.Start(); err != nil {
		_ = pipe.Close()
		return res, setFail(err)
	}
	done := false
	defer func() {
		_ = pipe.Close()
		if !done && encoder.Process != nil {
			_ = encoder.Process.Kill()
			_ = encoder.Wait()
		}
	}()
	for n := ch.First; n < ch.End; n++ {
		if err = ctx.Err(); err != nil {
			return res, setFail(err)
		}
		if failAtFrame != nil {
			if e := failAtFrame(ch.Index, n); e != nil {
				return res, setFail(e)
			}
		}
		t := float64(n) / float64(p.FPS)
		frames := map[string][]byte{}
		decodeAt := time.Now()
		for _, id := range sortedKeys(p.Tracks) {
			tr := p.Tracks[id]
			if !trackVisible(tr, n, p.FPS) {
				continue
			}
			index := trackFrame(tr, n, p.FPS)
			d := decoders[id]
			if d == nil {
				return res, setFail(fmt.Errorf("track %s: decoder missing", id))
			}
			b, e := d.get(index)
			if e != nil {
				return res, setFail(e)
			}
			frames[id] = b
			res.decodedPNG += int64(len(b))
		}
		res.decode.add(time.Since(decodeAt))
		shot, st, e := r.frameTimed(t, frames)
		if e != nil {
			return res, setFail(fmt.Errorf("frame %d: %w", n, e))
		}
		res.transfer.add(st.transfer)
		res.draw.add(st.draw)
		res.shot.add(st.screenshot)
		res.screenshotBytes += int64(len(shot))
		observeMu.Lock()
		if observeFrame != nil {
			observeFrame(n, t, shot)
		}
		observeMu.Unlock()
		writeAt := time.Now()
		if _, e = pipe.Write(shot); e != nil {
			return res, setFail(e)
		}
		res.write.add(time.Since(writeAt))
		k := int(completed.Add(1) - 1)
		if k%p.FPS == 0 {
			fmt.Fprintf(progress, ">> render %d/%d frames\n", k, nFrames)
		}
	}
	_ = pipe.Close()
	err = encoder.Wait()
	done = true
	if err != nil {
		return res, setFail(fmt.Errorf("encode: %w: %s", err, stderr.String()))
	}
	res.encode = roundSec(time.Since(encodeAt))
	res.wall = roundSec(time.Since(started))
	res.decoderStart = roundSec(decoderDur)
	res.files = r.filesForEvents(chunkEvents(p, ch)...)
	res.rendered = true
	return res, nil
}

func writeRenderFacts(ctx context.Context, p *Plan, dir string, files map[string]string, timings *renderTimings, started time.Time, versions map[string]string) error {
	metaAt := time.Now()
	inputs := map[string]string{}
	for rel, path := range p.Inputs {
		inputs[rel] = path
	}
	for rel, path := range files {
		if path != "" {
			inputs[rel] = path
		}
	}
	hashes := map[string]string{}
	for rel, path := range inputs {
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		h := sha256.New()
		_, err = io.Copy(h, f)
		_ = f.Close()
		if err != nil {
			return err
		}
		hashes[rel] = hex.EncodeToString(h.Sum(nil))
	}
	for _, name := range []string{"runtime.html", "template.html", "visual.html"} {
		b, err := web.ReadFile("web/" + name)
		if err != nil {
			return err
		}
		h := sha256.Sum256(b)
		hashes["builtin:"+name] = hex.EncodeToString(h[:])
	}
	if versions == nil {
		versions = map[string]string{}
	}
	b, err := command(ctx, "ffprobe", "-version").Output()
	if err != nil {
		return err
	}
	versions["ffprobe"] = strings.SplitN(string(b), "\n", 2)[0]
	timings.MetadataSeconds = roundSec(time.Since(metaAt))
	timings.TotalSeconds = roundSec(time.Since(started))
	data := map[string]any{"version": 1, "presentation": p.Name, "configuration": p.Document, "render": map[string]int{"w": p.Width, "h": p.Height, "fps": p.FPS, "frames": p.Frames}, "inputs": hashes, "tools": versions, "timings": timings}
	out, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "facts.json"), out, 0o644)
}
