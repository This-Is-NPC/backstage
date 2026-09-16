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
	"strconv"
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
	index                               int
	decode, transfer, draw, shot, write msHist
	decodedPNG, screenshotBytes         int64
	rendererStart, decoderStart         float64
	encode, wall                        float64
	files                               map[string]string
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
	if _, err = dependencies(); err != nil {
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
	nWorkers, err := resolveWorkers(planWorkers(p), nFrames, p.FPS)
	if err != nil {
		return err
	}
	chunks := splitChunks(opts.First, opts.End, nWorkers)
	if len(chunks) == 0 {
		return fmt.Errorf("interval has no frames")
	}
	nWorkers = len(chunks)
	timings.Workers = nWorkers
	_, _, encodeAll := resolveRenderThreads(planThreadCfg(p), len(p.Tracks))
	encodeThreads := chunkEncodeThreads(encodeAll, nWorkers)
	video := filepath.Join(work, "video.mp4")
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	results := make([]chunkResult, nWorkers)
	var completed atomic.Int64
	var fail firstFail
	fail.cancel = cancel
	var wg sync.WaitGroup
	for i := range chunks {
		wg.Add(1)
		go func(ch chunkRange) {
			defer wg.Done()
			res, e := renderChunk(ctx, runCtx, p, work, trackPaths, progress, opts, ch, nWorkers, nFrames, encodeThreads, &completed, &fail)
			if e != nil {
				return
			}
			results[ch.Index] = res
		}(chunks[i])
	}
	wg.Wait()
	if err = ctx.Err(); err != nil {
		return err
	}
	if fail.err != nil {
		return fmt.Errorf("chunk %d: %w", fail.idx, fail.err)
	}
	if nWorkers > 1 {
		concatAt := time.Now()
		if err = concatChunks(ctx, work, chunks, video); err != nil {
			return err
		}
		sec := roundSec(time.Since(concatAt))
		timings.ConcatSeconds = &sec
	}
	files := mergeChunkResults(results, &timings)
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
	}
	timings.CacheHits = cache.hits
	timings.CacheMisses = cache.misses
	collectWorkBytes(&timings, work, p, trackPaths)
	if err = writeRenderFacts(ctx, p, work, files, &timings, started); err != nil {
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

func encoderArgs(p *Plan, opts renderOpts, encodeThreads int, out string) []string {
	args := []string{"-v", "error", "-y", "-f", "image2pipe", "-framerate", strconv.Itoa(p.FPS), "-i", "-"}
	if opts.Scale != 1 {
		args = append(args, "-vf", draftCropFilter())
	}
	return append(args, "-an", "-c:v", "libx264", "-preset", "veryfast", "-crf", "20", "-pix_fmt", "yuv420p", "-threads", strconv.Itoa(encodeThreads), "-video_track_timescale", strconv.Itoa(p.FPS), out)
}

func mergeChunkResults(results []chunkResult, timings *renderTimings) map[string]string {
	files := map[string]string{}
	var decodeH, transferH, drawH, shotH, writeH msHist
	encodeMax := 0.0
	for _, res := range results {
		decodeH.merge(res.decode)
		transferH.merge(res.transfer)
		drawH.merge(res.draw)
		shotH.merge(res.shot)
		writeH.merge(res.write)
		timings.DecodedPNGBytes += res.decodedPNG
		timings.ScreenshotBytes += res.screenshotBytes
		timings.RendererStartSeconds += res.rendererStart
		timings.DecoderStartSeconds += res.decoderStart
		if res.encode > encodeMax {
			encodeMax = res.encode
		}
		timings.ChunkSeconds = append(timings.ChunkSeconds, namedSeconds{ID: fmt.Sprintf("chunk-%d", res.index), Seconds: res.wall})
		for rel, path := range res.files {
			files[rel] = path
		}
	}
	timings.EncodeSeconds = encodeMax
	timings.Decode = decodeH.snapshot()
	timings.Transfer = transferH.snapshot()
	timings.Draw = drawH.snapshot()
	timings.Screenshot = shotH.snapshot()
	timings.EncodeWrite = writeH.snapshot()
	return files
}

func renderChunk(parent, ctx context.Context, p *Plan, work string, trackPaths map[string]string, progress io.Writer, opts renderOpts, ch chunkRange, nChunks, nFrames, encodeThreads int, completed *atomic.Int64, fail *firstFail) (chunkResult, error) {
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
	rendererAt := time.Now()
	r, err := NewRenderer(ctx, p)
	if err != nil {
		return res, setFail(err)
	}
	r.scale = opts.Scale
	res.rendererStart = roundSec(time.Since(rendererAt))
	defer r.Close()
	decoders := map[string]*decoder{}
	defer func() {
		for _, d := range decoders {
			d.close()
		}
	}()
	decoderAt := time.Now()
	for _, id := range sortedKeys(p.Tracks) {
		tr := p.Tracks[id]
		if !trackNeeded(tr, ch.First, ch.End, p.FPS) {
			continue
		}
		index := trackIndexAt(tr, ch.First, p.FPS)
		d, e := openTrackDecoder(ctx, trackPaths[id], index, p.FPS)
		if e != nil {
			return res, setFail(e)
		}
		decoders[id] = d
	}
	res.decoderStart = roundSec(time.Since(decoderAt))
	out := chunkVideoPath(work, nChunks, ch.Index)
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
			b, e := decoders[id].get(index)
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
	res.files = r.loadedFiles()
	fmt.Fprintf(progress, ">> chunk-%d ok\n", ch.Index)
	return res, nil
}

func writeRenderFacts(ctx context.Context, p *Plan, dir string, files map[string]string, timings *renderTimings, started time.Time) error {
	metaAt := time.Now()
	inputs := map[string]string{}
	for rel, path := range p.Inputs {
		inputs[rel] = path
	}
	for rel, path := range files {
		inputs[rel] = path
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
	versions := map[string]string{}
	browser, _ := dependencies()
	for _, name := range []string{browser, "ffmpeg", "ffprobe"} {
		flag := "-version"
		if name == browser {
			flag = "--version"
		}
		b, err := command(ctx, name, flag).Output()
		if err != nil {
			return err
		}
		versions[filepath.Base(name)] = strings.SplitN(string(b), "\n", 2)[0]
	}
	timings.MetadataSeconds = roundSec(time.Since(metaAt))
	timings.TotalSeconds = roundSec(time.Since(started))
	data := map[string]any{"version": 1, "presentation": p.Name, "configuration": p.Document, "render": map[string]int{"w": p.Width, "h": p.Height, "fps": p.FPS, "frames": p.Frames}, "inputs": hashes, "tools": versions, "timings": timings}
	b, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "facts.json"), b, 0o644)
}
