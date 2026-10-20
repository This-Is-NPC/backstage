import React from 'react';
import {AbsoluteFill} from 'remotion';

// Replace this component with real application components. Step and cue times
// have already been mapped through Backstage's cuts, repeats and playback rates.
export const Stage = ({data, event, steps, cues}) => {
  const card = event?.card;
  return <AbsoluteFill style={{pointerEvents: 'none', justifyContent: 'center', alignItems: 'center', textAlign: 'center', padding: '8%'}}>
    {card && <section style={{background: data.theme.background, padding: '1em', borderRadius: '0.3em'}}>
      <h1 style={{fontSize: data.theme['title-size'], margin: 0}}>{card.title}</h1>
      {[card.subtitle, card.text, card.url].filter(Boolean).map((text, index) =>
        <p key={index} style={{fontSize: data.theme['text-size']}}>{text}</p>)}
    </section>}
    {(cues.length > 0 || steps.length > 0) && <div style={{position: 'absolute', bottom: '5%', maxWidth: '85%', background: data.theme.background, padding: '0.5em 1em', borderRadius: '0.4em', fontSize: data.theme['caption-size']}}>
      {cues.map((cue, index) => <div key={index}>{cue.text}</div>)}
      {steps.filter((step) => step.keys?.length).map((step, index) => <div key={index} style={{color: data.theme.accent}}>{step.keys.join(' → ')}</div>)}
    </div>}
  </AbsoluteFill>;
};
