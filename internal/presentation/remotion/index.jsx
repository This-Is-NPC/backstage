import React from 'react';
import {AbsoluteFill, Composition, Freeze, Html5Audio, OffthreadVideo, Sequence,
  registerRoot, staticFile, useCurrentFrame, useVideoConfig} from 'remotion';
import bundle from './backstage.json';
import {Stage} from './stage.jsx';

const Film = (data) => {
  const frame = useCurrentFrame();
  const {fps} = useVideoConfig();
  const time = frame / fps;
  const event = data.document.timeline.findLast((entry) => entry.at <= time);
  const visible = [...new Set(Object.values(event?.slots ?? {}))];
  const steps = Object.entries(data.tracks).flatMap(([track, value]) =>
    value.steps.filter((step) => time >= step.start && time < step.end).map((step) => ({...step, track})));
  const cues = Object.entries(data.tracks).flatMap(([track, value]) =>
    value.cues.filter((cue) => time >= cue.start && time < cue.end).map((cue) => ({...cue, track})));
  return <AbsoluteFill style={{background: data.theme.background, color: data.theme.foreground, fontFamily: data.theme.font}}>
    <AbsoluteFill style={{display: 'flex', flexDirection: 'row'}}>
      {visible.map((id) => {
        const track = data.tracks[id];
        if (!track || frame < track.from || (track['on-end'] === 'hide' && frame >= track.end)) return null;
        return <div key={id} style={{position: 'relative', flex: 1}}>
          <Sequence from={track.from}>
            <Freeze frame={Math.min(frame - track.from, track.frames - 1)}>
              <OffthreadVideo src={staticFile(track.media)} muted style={{width: '100%', height: '100%', objectFit: 'contain'}} />
            </Freeze>
          </Sequence>
        </div>;
      })}
    </AbsoluteFill>
    {data.audio && <Html5Audio src={staticFile(data.audio)} />}
    <Stage data={data} event={event} time={time} steps={steps} cues={cues} />
  </AbsoluteFill>;
};

registerRoot(() => <Composition id="Backstage" component={Film} defaultProps={bundle}
  width={bundle.width} height={bundle.height} fps={bundle.fps} durationInFrames={bundle.frames} />);
