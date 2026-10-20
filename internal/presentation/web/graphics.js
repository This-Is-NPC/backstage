// Motion is evaluated from the render clock, including seeks and previews.
const motion = {
  ease(p, name) {
    p = Math.max(0, Math.min(1, p));
    if (name === 'linear') return p;
    if (name === 'ease-in-out') return p < 0.5 ? 4*p*p*p : 1-Math.pow(-2*p+2, 3)/2;
    return 1-Math.pow(1-p, 3);
  },
  progress(time, duration, ease) { return this.ease(duration > 0 ? time/duration : 1, ease); },
  apply(el, spec, time) {
    const effect = spec?.effect || 'none';
    const p = this.progress(time, spec?.duration || 0, spec?.ease || config.theme.ease);
    if (effect === 'rise' || effect === 'fade' || effect === 'scale') el.style.opacity = String(p);
    if (effect === 'rise') el.style.transform = `translateY(${24*(1-p)}px)`;
    if (effect === 'scale') el.style.transform = `scale(${0.92+0.08*p})`;
    return p;
  },
  entrance(el, text, spec, time) {
    const p = this.apply(el, spec, time);
    el.textContent = spec?.effect === 'type' ? Array.from(text).slice(0, Math.floor(Array.from(text).length*p)).join('') : text;
  },
  strike(el, time) {
    const line = document.createElement('span');
    Object.assign(line.style, {position:'absolute',left:0,right:0,top:'53%',height:'0.07em',background:config.theme.accent,transformOrigin:'left',transform:`scaleX(${this.progress(time,0.35,config.theme.ease)})`});
    el.append(line);
  }
};

function clearGraphics() {
  document.querySelectorAll('.graphic-event').forEach(el => el.remove());
}

function applyTransition(el, incoming, p, transition) {
  const effect = transition.effect, direction = transition.direction || 'left';
  const x = direction === 'left' ? -1 : direction === 'right' ? 1 : 0;
  const y = direction === 'up' ? -1 : direction === 'down' ? 1 : 0;
  if (effect === 'slide' || effect === 'push') {
    el.style.opacity = '1';
    if (effect === 'push') {
      // xfade computes its slide offset in float32, then truncates to pixels.
      const size = x ? document.documentElement.clientWidth : document.documentElement.clientHeight;
      const remaining = Math.trunc(Math.fround(Math.fround(1-Math.fround(p))*size));
      const shift = incoming ? -remaining : size-remaining;
      el.style.transform = `translate(${x*shift}px,${y*shift}px)`;
    } else {
      const shift = incoming ? p-1 : 0;
      el.style.transform = `translate(${x*shift*100}%,${y*shift*100}%)`;
    }
  } else if (effect === 'wipe') {
    el.style.opacity = '1';
    if (incoming) {
      const inset = (1-p)*100;
      el.style.clipPath = direction === 'left' ? `inset(0 0 0 ${inset}%)` : direction === 'right' ? `inset(0 ${inset}% 0 0)` : direction === 'up' ? `inset(${inset}% 0 0 0)` : `inset(0 0 ${inset}% 0)`;
    }
  } else if (effect === 'zoom') {
    if (incoming) el.style.transform = `scale(${0.8+0.2*p})`;
  } else if (effect === 'blur') {
    el.style.filter = `blur(${12*(incoming ? 1-p : p)}px)`;
  }
}

function drawGraphics(index, opacity, time) {
  const event = config.events[index], theme = config.theme;
  const root = document.createElement('div');
  root.className = 'graphic-event';
  Object.assign(root.style, {position:'absolute',inset:0,pointerEvents:'none',opacity:String(opacity)});
  document.body.append(root);
  const groups = new Map();
  for (const caption of config.text) {
    if (time < caption.at || time >= caption.end) continue;
    if (!groups.has(caption.slot)) groups.set(caption.slot, []);
    groups.get(caption.slot).push(caption);
  }
  for (const [slot, captions] of groups) {
    const source = find(frames[index], 'data-caption-slot', slot);
    if (!source) continue;
    const rect = source.getBoundingClientRect(), style = frames[index].contentWindow.getComputedStyle(source);
    const box = document.createElement('div');
    box.className = 'caption';
    Object.assign(box.style, {left:rect.x+'px',top:rect.y+'px',width:rect.width+'px',height:rect.height+'px',font:style.font,color:style.color,textAlign:style.textAlign,lineHeight:style.lineHeight,background:style.backgroundColor});
    if (captions.every(c => (!c.entrance?.effect || c.entrance.effect === 'none') && !c['font-size'])) {
      box.textContent = captions.map(c => c.text).join('\n');
    } else {
      for (const caption of captions) {
        const line = document.createElement('div');
        if (caption['font-size']) Object.assign(line.style,{fontSize:caption['font-size']+'px',fontFamily:theme.font,color:theme.foreground});
        motion.entrance(line, caption.text, caption.entrance, time-caption.at);
        box.append(line);
      }
    }
    root.append(box);
  }
  drawActions(root,index,time);
  if (!event.card) return root;
  const card = event.card, box = document.createElement('section');
  box.className = 'card';
  Object.assign(box.style, {position:'absolute',left:'10%',right:'10%',top:'22%',color:theme.foreground,fontFamily:theme.font,textAlign:'center',lineHeight:'1.2',boxSizing:'border-box'});
  if (card.type === 'lower-third') Object.assign(box.style,{top:'auto',bottom:'14%',textAlign:'left'});
  if (card.type === 'badge') Object.assign(box.style,{left:'auto',right:'8%',top:'8%',background:theme.accent,color:theme.background,padding:'0.35em 0.7em',borderRadius:'0.3em',fontSize:theme['text-size']+'px'});
  const local = time-event.at;
  motion.apply(box,card.entrance,local);
  if (card.logo) {
    const logo = document.createElement('img');
    logo.src = card.logo;
    Object.assign(logo.style,{display:'block',margin:'0 auto 24px',maxWidth:'30%',maxHeight:'18vh'});
    box.append(logo);
  }
  const title = document.createElement('div'), titleText = document.createElement('span');
  Object.assign(title.style,{fontSize:(card.type === 'badge' || card.type === 'lower-third' ? theme['text-size'] : theme['title-size'])+'px',fontWeight:'700'});
  Object.assign(titleText.style,{position:'relative',display:'inline-block'});
  motion.entrance(titleText,card.title,card.entrance.effect === 'type' ? card.entrance : {effect:'none'},local);
  if (card.strike) motion.strike(titleText,local-card.entrance.duration);
  title.append(titleText); box.append(title);
  for (const value of [card.subtitle, card.text, card.url]) {
    if (!value) continue;
    const line = document.createElement('div');
    line.textContent = value;
    Object.assign(line.style,{fontSize:theme['text-size']+'px',marginTop:'0.6em',overflowWrap:'anywhere'});
    box.append(line);
  }
  root.append(box);
  return root;
}

window.lintTypography = function() {
  const problems = new Set();
  for (const caption of config.text) {
    for (let index=0;index<config.events.length;index++) {
      const event=config.events[index],end=config.events[index+1]?.at??config.duration;
      if(caption.at>=end || caption.end<=event.at)continue;
      const slot=find(frames[index],'data-caption-slot',caption.slot);
      if(!slot)continue;
      const size=caption['font-size'] || parseFloat(frames[index].contentWindow.getComputedStyle(slot).fontSize);
      if(size<config.theme['min-font'])problems.add(`caption ${caption.slot} in event ${index} uses ${size}px; minimum is ${config.theme['min-font']}px`);
    }
  }
  return [...problems];
};

function cameraRegion(camera, time) {
  let region={x:0,y:0,w:1,h:1};
  const keys=camera?.keyframes||[];
  if(!keys.length)return null;
  for(let i=0;i<keys.length;i++) {
    const key=keys[i];
    if(time>=key.at){region=key.region;continue;}
    if(i>0 && key.ease!=='cut') {
      const before=keys[i-1],p=motion.progress(time-before.at,key.at-before.at,key.ease);
      region={};
      for(const axis of ['x','y','w','h'])region[axis]=before.region[axis]+(key.region[axis]-before.region[axis])*p;
    }
    break;
  }
  return region;
}

function tracePointer(action,time) {
  const trace=action['pointer-trace'];
  if(!trace?.length)return action.pointer;
  const source=(action['source-start']??0)+(time-action.at)*action.rate;
  let lo=0,hi=trace.length;
  while(lo<hi){const mid=(lo+hi)>>>1;if(trace[mid].at<=source)lo=mid+1;else hi=mid;}
  const before=trace[lo-1],after=trace[lo];
  if(!before?.pointer || source-before.at>0.12)return null;
  let {x,y}=before.pointer;
  if(after?.pointer && after.at-before.at<=0.12) {
    const progress=(source-before.at)/(after.at-before.at);
    x+=(after.pointer.x-x)*progress;y+=(after.pointer.y-y)*progress;
  }
  let clickAt=null;
  for(let i=lo-1;i>=0 && trace[i].at>=(action['source-start']??0) && source-trace[i].at<0.35*action.rate;i--) {
    if(!trace[i].pointer)break;
    if(i<lo-1 && trace[i+1].at-trace[i].at>0.12)break;
    if(trace[i].pointer.click){clickAt=action.at+(trace[i].at-(action['source-start']??0))/action.rate;break;}
  }
  return {x,y,click:clickAt!==null,clickAt};
}

function drawActions(root,index,time) {
  const keys=[];
  for(const action of config.actions||[]) {
    if(time<action.at || time>=action.end)continue;
    const raw=geometry(index,action.track),size=config.sizes[action.track];
    if(!raw || !size || !size[0] || !size[1])continue;
    if(action.keys?.length)keys.push(action.keys.join(' → '));
    const pointer=tracePointer(action,time);
    if(!action.region && !pointer)continue;
    const box=snapGeo(raw),cc=contentSnap(raw),p=picturePlacement(size[0],size[1],raw,box);
    const ox=box.x+(raw.bl||0)+p.x-cc.cx,oy=box.y+(raw.bt||0)+p.y-cc.cy;
    const viewport=document.createElement('div');
    const vx=ox+p.cropX,vy=oy+p.cropY;
    Object.assign(viewport.style,{position:'absolute',left:cc.cx+'px',top:cc.cy+'px',width:cc.cw+'px',height:cc.ch+'px',overflow:'hidden',borderRadius:Math.max(0,(raw.rtl||0)-(raw.bl||0))+'px',clipPath:`inset(${Math.max(0,vy)}px ${Math.max(0,cc.cw-vx-p.regionW)}px ${Math.max(0,cc.ch-vy-p.regionH)}px ${Math.max(0,vx)}px)`});
    root.append(viewport);
    if(action.region) {
      const region=action.region,mark=document.createElement('div');
      mark.className='action-highlight';
      Object.assign(mark.style,{position:'absolute',left:(ox+region.x*p.w)+'px',top:(oy+region.y*p.h)+'px',width:(region.w*p.w)+'px',height:(region.h*p.h)+'px',boxSizing:'border-box',border:'3px solid '+config.theme.accent,borderRadius:'8px'});
      if(action.highlight==='spotlight')mark.style.boxShadow='0 0 0 100vmax #0009';
      viewport.append(mark);
    }
    if(pointer) {
      const ring=document.createElement('div');
      ring.className='action-pointer';
      const clickAt=pointer.clickAt??action.at;
      const clicking=pointer.click && time<clickAt+0.35;
      const progress=clicking?motion.progress(time-clickAt,0.35,'linear'):0;
      const radius=clicking?12+30*progress:16;
      Object.assign(ring.style,{position:'absolute',left:(ox+pointer.x*p.w-radius)+'px',top:(oy+pointer.y*p.h-radius)+'px',width:2*radius+'px',height:2*radius+'px',boxSizing:'border-box',border:'3px solid '+config.theme.accent,borderRadius:'50%',background:'transparent',opacity:String(clicking?1-progress:1)});
      viewport.append(ring);
    }
  }
  if(keys.length) {
    const box=document.createElement('div');
    box.className='action-keys';
    box.textContent=keys.join(' · ');
    Object.assign(box.style,{position:'absolute',left:'50%',bottom:'5%',transform:'translateX(-50%)',maxWidth:'90%',padding:'0.4em 0.7em',border:'2px solid '+config.theme.accent,borderRadius:'0.4em',background:config.theme.background,color:config.theme.foreground,fontFamily:config.theme.font,fontSize:config.theme['caption-size']+'px',textAlign:'center',overflowWrap:'anywhere'});
    root.append(box);
  }
}
