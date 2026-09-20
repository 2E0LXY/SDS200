const $ = s => document.querySelector(s);
const $$ = s => [...document.querySelectorAll(s)];
const pages = $('#pages');
const clientId = localStorage.sdsClientId || (localStorage.sdsClientId = crypto.randomUUID());
let lease = false, stateCache = {}, fqk = Array(100).fill(0), config = {}, capabilities = {};
let waterfallTimer = null, waterfallRunning = false, lastWfTimestamp = 0, lastSelectedHost = '', activePage = 0, fqkEditMode = false;
let memoryStack = [];

function headers(control=false){const h={'Content-Type':'application/json'};if(control){h['X-Client-Id']=clientId;const t=$('#token').value||localStorage.sdsToken||'';if(t)h['X-Control-Token']=t;}return h}
async function api(path, opts={}){const r=await fetch(path,opts);let data;try{data=await r.json()}catch{data={detail:await r.text()}}if(!r.ok)throw new Error(data.detail||`${r.status}`);return data}
function log(msg,level='INFO',component='BROWSER'){
  const el=$('#log');
  const line=`${new Date().toLocaleTimeString()}  ${String(level).toUpperCase().padEnd(5)} ${String(component).toUpperCase().padEnd(10)} ${msg}`;
  if(el)el.textContent=line+'\n'+el.textContent.slice(0,24000);
  fetch('/api/logs/client',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({level,message:String(msg),component})}).catch(()=>{});
}
function text(id,v,fallback='—'){const el=$(id);if(el)el.textContent=(v===undefined||v===null||v==='')?fallback:v}
function held(v){return ['on','1','true','held'].includes(String(v??'').toLowerCase())}

$$('#tabs button').forEach(b=>b.onclick=()=>{activePage=Number(b.dataset.page);pages.scrollTo({left:pages.clientWidth*activePage,behavior:'smooth'});if(activePage===2)readDisplay()});
pages.addEventListener('scroll',()=>{const i=Math.round(pages.scrollLeft/pages.clientWidth);activePage=i;$$('#tabs button').forEach((b,n)=>b.classList.toggle('active',n===i))},{passive:true});

function goToPage(index){activePage=Number(index);pages.scrollTo({left:pages.clientWidth*activePage,behavior:'smooth'});if(activePage===2)setTimeout(readDisplay,120)}
$$('[data-goto]').forEach(b=>b.onclick=()=>goToPage(b.dataset.goto));
$$('[data-dashboard-rf]').forEach(b=>b.addEventListener('click',()=>{setTimeout(()=>{const target=$(`[data-rf-view="${b.dataset.dashboardRf}"]`);if(target)target.click()},260)}));
async function pressSequence(seq,label='Key sequence'){
  const keys=String(seq||'').split(',').map(x=>x.trim()).filter(Boolean);
  if(!keys.length)return;
  try{
    for(const k of keys){const d=await control('/api/control/key',{key:k});if(d.display)renderDisplay(d.display);await new Promise(r=>setTimeout(r,110))}
    log(`${label} sent`,'INFO','CONTROL');setTimeout(()=>refresh(),120)
  }catch(e){log(`${label}: ${e.message}`)}
}
$$('[data-sequence]').forEach(b=>b.onclick=()=>pressSequence(b.dataset.sequence,b.dataset.sequenceLabel||b.textContent.trim()));
$$('[data-open-menu]').forEach(b=>b.onclick=async()=>{await pressSequence('M','Menu');goToPage(2)});

$$('[data-rf-view]').forEach(b=>b.onclick=()=>{$$('[data-rf-view]').forEach(x=>x.classList.toggle('selected',x===b));$$('[data-rf-panel]').forEach(p=>p.classList.toggle('active',p.dataset.rfPanel===b.dataset.rfView))});

function renderSignal(s){let level=Number(s.signal);if(!Number.isFinite(level)){const r=Number(s.rssi);level=Number.isFinite(r)?Math.max(0,Math.min(5,Math.round((r+120)/10))):0}$$('#signalBars i').forEach((bar,i)=>bar.classList.toggle('on',i<level));$$('#dashSignalBars i').forEach((bar,i)=>bar.classList.toggle('on',i<level));text('#rssi',s.rssi?`${s.rssi} dBm`:`Signal ${level}`);text('#dashRssi',s.rssi?`${s.rssi} dBm`:`Signal ${level}`)}
function renderHold(scope,value){const id=`#${scope}Hold`;text(id,held(value)?'HELD':'SCAN');$$(`[data-hold="${scope}"]`).forEach(b=>b.classList.toggle('held',held(value)))}
function render(s){
  stateCache=s;
  const c=$('#connection');c.textContent=s.online?'ONLINE':'OFFLINE';c.className=`status ${s.online?'online':'offline'}`;
  text('#headline',s.online?`${s.system||'SDS200'} • ${s.channel||s.mode||''}`:(s.error||'Scanner unavailable'));
  text('#mode',s.mode);text('#liveMode',s.mode||'SCANNER');text('#channel',s.channel);text('#frequency',formatDisplayFrequency(s.frequency));text('#tgid',s.tgid);text('#unitId',s.unit_id);text('#modulation',s.modulation||s.p25_status);text('#serviceType',s.service_type);text('#breadcrumb',[s.system,s.department,s.site].filter(Boolean).join('  ›  '));
  renderSignal(s);renderHold('system',s.system_hold);renderHold('department',s.department_hold);renderHold('site',s.site_hold);renderHold('channel',s.channel_hold);
  if(Number.isInteger(s.volume)){ $('#volume').value=s.volume;text('#volumeOut',s.volume);$('#dashVolume').value=s.volume;text('#dashVolumeOut',s.volume)}
  if(Number.isInteger(s.squelch)){ $('#squelch').value=s.squelch;text('#squelchOut',s.squelch);$('#dashSquelch').value=s.squelch;text('#dashSquelchOut',s.squelch)}
  text('#recording',s.recording);text('#model',s.model);text('#firmware',s.firmware);text('#latency',s.latency_ms?`${s.latency_ms} ms`:'—');text('#scannerHost',`${config.host||''}:${config.udp_port||''}`);
  text('#dashOnline',s.online?'CONNECTED':'OFFLINE');$('#dashOnline').style.color=s.online?'#61f298':'#ff8794';
  const dashTitle=s.channel||(s.frequency?formatDisplayFrequency(s.frequency):'')||s.mode||'Waiting for scanner';const dashSub=[s.system,s.department,s.site].filter(Boolean).join(' › ')||(dashTitle===s.mode?'':s.mode);text('#dashChannel',dashTitle);text('#dashSystem',dashSub,'');text('#dashSystem2',s.system);text('#dashDepartment',s.department);text('#dashSite',s.site);text('#dashFrequency',formatDisplayFrequency(s.frequency));text('#dashTgid',s.tgid);text('#dashUnitId',s.unit_id);text('#dashModulation',s.modulation||s.p25_status);text('#dashService',s.service_type);text('#dashScanMode',s.mode||'—');text('#dashHoldState',`Channel: ${held(s.channel_hold)?'HOLD':'SCAN'}`);
  text('#dashScannerRecording',s.recording||'—');text('#dashModel',s.model||'SDS200');text('#dashFirmware',s.firmware);text('#dashHost',config.host?`${config.host}:${config.udp_port||50536}`:'No scanner selected');text('#dashLatency',s.latency_ms?`${s.latency_ms} ms`:'—');
}
function formatDisplayFrequency(v){if(v===null||v===undefined||v==='')return '—';const s=String(v).trim().replace(/\s*MHz$/i,'');if(s.includes('.')){const f=Number(s);return Number.isFinite(f)?`${f.toFixed(4)} MHz`:`${s} MHz`}const n=Number(s);if(!Number.isFinite(n))return s;return n>=250000&&n<=13000000?`${(n/10000).toFixed(4)} MHz`:s}
async function refresh(force=false){try{render(await api('/api/state'+(force?'?force=1':'')))}catch(e){render({online:false,error:e.message})}}
async function control(path,body={}){if(!lease){await acquire();if(!lease)throw new Error('Another operator currently has scanner control')}return api(path,{method:'POST',headers:headers(true),body:JSON.stringify(body)})}

$('#volume').oninput=e=>text('#volumeOut',e.target.value);$('#squelch').oninput=e=>text('#squelchOut',e.target.value);
$('#dashVolume').oninput=e=>text('#dashVolumeOut',e.target.value);$('#dashSquelch').oninput=e=>text('#dashSquelchOut',e.target.value);
$('#volume').onchange=async e=>{try{await control('/api/control/volume',{level:Number(e.target.value)});log(`Volume ${e.target.value}`);await refresh()}catch(err){log(err.message)}};
$('#squelch').onchange=async e=>{try{await control('/api/control/squelch',{level:Number(e.target.value)});log(`Squelch ${e.target.value}`);await refresh()}catch(err){log(err.message)}};
$('#dashVolume').onchange=async e=>{try{await control('/api/control/volume',{level:Number(e.target.value)});log(`Volume ${e.target.value}`);await refresh()}catch(err){log(err.message)}};
$('#dashSquelch').onchange=async e=>{try{await control('/api/control/squelch',{level:Number(e.target.value)});log(`Squelch ${e.target.value}`);await refresh()}catch(err){log(err.message)}};
$$('[data-key]').forEach(b=>b.onclick=async()=>{
  try{
    const key=b.dataset.key;
    const d=await control('/api/control/key',{key});
    log(`${capabilities.key_codes?.[key]||'Key'} (${key}) • ${String(d.raw||'OK').trim()}`,'INFO','CONTROL');
    if(d.display)renderDisplay(d.display);else await readDisplay();
    setTimeout(()=>refresh(),120);
  }catch(e){log(e.message,'ERROR','CONTROL')}
});

async function setHold(scope, enabled){try{const d=await control(`/api/control/hold/${scope}`,{enabled});log(`${scope} hold ${enabled?'ON':'OFF'}${d.changed?'':' (already set)'}`);await refresh()}catch(e){log(e.message)}}
$$('[data-hold]').forEach(b=>b.onclick=()=>setHold(b.dataset.hold,!held(stateCache[`${b.dataset.hold}_hold`])));
$('#liveHold').onclick=()=>setHold('channel',!held(stateCache.channel_hold));

function channelNavTarget(){return {TGID:'TGID',ConvFrequency:'CFREQ',WxChannel:'WX',ToneOutChannel:'FTO',CcHitsChannel:'CCHIT',SrchFrequency:'QS_FREQ'}[stateCache.channel_kind]||null}
async function nav(direction){const target=channelNavTarget();if(!target||stateCache.channel_index===null||stateCache.channel_index===undefined){try{const d=await control('/api/control/key',{key:direction==='next'?'>':'<'});if(d.display)renderDisplay(d.display);log(`${direction} (rotary — no channel index in this mode)`,'INFO','CONTROL');setTimeout(refresh,250)}catch(e){log(e.message)}return}try{await control(`/api/nav/${direction}`,{target,first:stateCache.channel_index,count:1});log(`${direction} ${target}`);setTimeout(refresh,250)}catch(e){log(e.message)}}
$('#liveNext').onclick=()=>nav('next');$('#livePrevious').onclick=()=>nav('previous');
$('#dashHold').onclick=()=>setHold('channel',!held(stateCache.channel_hold));$('#dashNext').onclick=()=>nav('next');$('#dashPrevious').onclick=()=>nav('previous');

function renderFqk(){const g=$('#fqkGrid');g.innerHTML='';fqk.forEach((st,i)=>{const b=document.createElement('button');b.textContent=String(i).padStart(2,'0');b.dataset.state=st;b.title=fqkEditMode?`Edit FQK ${i}`:`Open memory assigned to FQK ${i}`;b.disabled=st===0;if(fqkEditMode){b.onclick=async()=>{const old=fqk[i];fqk[i]=old===2?1:2;renderFqk();try{await control('/api/fqk',{states:fqk});log(`FQK ${String(i).padStart(2,'0')} ${fqk[i]===2?'ON':'OFF'}`);await loadFqk()}catch(e){log(e.message);fqk[i]=old;renderFqk()}}}else{b.onclick=()=>memoryByQuickKey(i)}g.appendChild(b)})}
function renderDashFqk(){const g=$('#dashFqkGrid');if(!g)return;g.innerHTML='';fqk.slice(0,20).forEach((st,i)=>{const b=document.createElement('button');b.textContent=`${String(i).padStart(2,'0')} ${st===2?'ON':st===1?'OFF':'—'}`;b.dataset.state=st;b.disabled=st===0;b.title=st===0?`FQK ${i} not assigned`:`Toggle FQK ${i}`;b.onclick=async()=>{if(st===0)return;const old=fqk[i];fqk[i]=old===2?1:2;renderFqk();renderDashFqk();try{await control('/api/fqk',{states:fqk});log(`FQK ${String(i).padStart(2,'0')} ${fqk[i]===2?'ON':'OFF'}`);await loadFqk()}catch(e){fqk[i]=old;renderFqk();renderDashFqk();log(e.message)}};g.appendChild(b)})}
async function loadFqk(){try{const d=await api('/api/fqk');if(d.states?.length===100)fqk=d.states;renderFqk();renderDashFqk()}catch(e){log(`FQK: ${e.message}`)}}
$('#refreshFqk').onclick=loadFqk;
$('#fqkEditMode').onclick=()=>{fqkEditMode=!fqkEditMode;$('#fqkEditMode').textContent=fqkEditMode?'Disable FQK editing':'Enable FQK editing';log(fqkEditMode?'FQK editing enabled':'FQK editing disabled');renderFqk()};
async function loadFavorites(){try{const d=await api('/api/favorites');const rows=(d.records||[]).filter(r=>r.Name||r.name);const out=$('#favoritesList');out.innerHTML='';if(!rows.length){out.innerHTML='<div class="empty">No named favorites returned.</div>'}else{rows.slice(0,100).forEach(r=>{const el=document.createElement('div');el.className='list-row memory-row';el.innerHTML=`<span class="led"></span><div><strong>${escapeHtml(r.Name||r.name)}</strong><div class="notice">${escapeHtml(r.tag||'Favorite')} • Monitor ${escapeHtml(r.Monitor||'')}</div></div><span>${escapeHtml(r.Q_Key??r.QKey??'')}</span>`;el.onclick=()=>memoryShow({type:'level',kind:'SYS',parent:String(r.Index),title:r.Name||'Favorite'});out.appendChild(el)})}
  const dash=$('#dashFavorites');dash.innerHTML='';const active=rows.filter(r=>String(r.Monitor||'').toLowerCase()!=='off').slice(0,3);if(!active.length){dash.innerHTML='<div class="empty">No active monitor lists reported.</div>'}else active.forEach(r=>{const el=document.createElement('div');el.className='dash-monitor-row';el.innerHTML=`<i></i><strong>${escapeHtml(r.Name||r.name)}</strong><span>QK ${escapeHtml(r.Q_Key??r.QKey??'—')}</span>`;el.onclick=()=>{memoryShow({type:'level',kind:'SYS',parent:String(r.Index),title:r.Name||'Favorite'});goToPage(3)};dash.appendChild(el)})
}catch(e){log(`Favorites: ${e.message}`)}}
$('#refreshFavorites').onclick=loadFavorites;

function memoryLabel(r){return r.Name||r.name||r.Freq||r.TGID||r.Index||r.tag||'Item'}
function memoryMeta(r){return [r.tag,r.Type,r.SystemType,r.Freq,r.Mod,r.TGID,r.SvcType,r.Avoid?`Avoid ${r.Avoid}`:'',r.Q_Key&&r.Q_Key!=='None'?`QK ${r.Q_Key}`:''].filter(Boolean).join(' • ')}
function setMemoryRows(records,onClick){const out=$('#memoryList');out.innerHTML='';if(!records.length){out.innerHTML='<div class="empty">No entries returned at this memory level.</div>';return}records.forEach(r=>{const el=document.createElement('div');el.className='list-row memory-row'+(onClick?'':' terminal');el.innerHTML=`<span class="led"></span><div><strong>${escapeHtml(memoryLabel(r))}</strong><div class="meta">${escapeHtml(memoryMeta(r))}</div></div><span>${escapeHtml(r.Index??'')}</span>`;if(onClick)el.onclick=()=>onClick(r);out.appendChild(el)})}
function memoryCrumbs(){text('#memoryBreadcrumb',memoryStack.map(x=>x.title).join(' › ')||'Favorites')}
async function memoryFetch(kind,parent=''){const q=new URLSearchParams({kind});if(parent!==''&&parent!==null)q.set('parent',parent);return api(`/api/memory?${q}`)}
async function memoryShow(step,push=true){try{if(push){if(!memoryStack.length&&step.kind!=='FL')memoryStack=[{type:'level',kind:'FL',title:'Favorites'}];memoryStack.push(step)}memoryCrumbs();if(step.type==='level'){const d=await memoryFetch(step.kind,step.parent||'');const rec=d.records||[];if(step.kind==='FL')setMemoryRows(rec,r=>memoryShow({type:'level',kind:'SYS',parent:String(r.Index),title:r.Name||'Favorite'}));else if(step.kind==='SYS')setMemoryRows(rec,r=>memoryShow({type:'system',index:String(r.Index),title:r.Name||'System'}));else if(step.kind==='SFREQ')setMemoryRows(rec,null);else setMemoryRows(rec,null)}else if(step.type==='system'){const [deps,sites]=await Promise.all([memoryFetch('DEPT',step.index),memoryFetch('SITE',step.index)]);const rows=[...(deps.records||[]).map(x=>({...x,_branch:'dept'})),...(sites.records||[]).map(x=>({...x,_branch:'site'}))];setMemoryRows(rows,r=>r._branch==='dept'?memoryShow({type:'department',index:String(r.Index),title:r.Name||'Department'}):memoryShow({type:'level',kind:'SFREQ',parent:String(r.Index),title:r.Name||'Site'}))}else if(step.type==='department'){const [cf,tg]=await Promise.all([memoryFetch('CFREQ',step.index),memoryFetch('TGID',step.index)]);setMemoryRows([...(cf.records||[]),...(tg.records||[])],null)}}catch(e){log(`Memory: ${e.message}`);$('#memoryList').innerHTML=`<div class="empty">${escapeHtml(e.message)}</div>`}}
async function memoryHome(){memoryStack=[];const step={type:'level',kind:'FL',title:'Favorites'};memoryStack=[step];await memoryShow(step,false)}
$('#memoryHome').onclick=memoryHome;
$('#memoryBack').onclick=async()=>{if(memoryStack.length<=1){await memoryHome();return}memoryStack.pop();const step=memoryStack[memoryStack.length-1];await memoryShow(step,false)};
async function memoryByQuickKey(qk){try{const d=await memoryFetch('FL');const matches=(d.records||[]).filter(r=>String(r.Q_Key??r.QKey??'')===String(qk));memoryStack=[{type:'level',kind:'FL',title:'Favorites'}];text('#memoryBreadcrumb',`Favorites › FQK ${String(qk).padStart(2,'0')}`);if(!matches.length){setMemoryRows([],null);return}setMemoryRows(matches,r=>memoryShow({type:'level',kind:'SYS',parent:String(r.Index),title:r.Name||'Favorite'}));pages.scrollTo({left:pages.clientWidth*3,behavior:'smooth'});activePage=3}catch(e){log(`Memory: ${e.message}`)}}
function escapeHtml(s){return String(s??'').replace(/[&<>"]/g,c=>({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;'}[c]))}

const spectrum=$('#spectrum'), spectrumCtx=spectrum.getContext('2d'), wf=$('#waterfall'), wfCtx=wf.getContext('2d'), dashSpectrum=$('#dashSpectrum'), dashSpectrumCtx=dashSpectrum.getContext('2d'), dashWf=$('#dashWaterfall'), dashWfCtx=dashWf.getContext('2d');
function drawGrid(ctx,w,h){ctx.clearRect(0,0,w,h);ctx.strokeStyle='#13354b';ctx.lineWidth=1;for(let x=0;x<=w;x+=w/8){ctx.beginPath();ctx.moveTo(x,0);ctx.lineTo(x,h);ctx.stroke()}for(let y=0;y<=h;y+=h/5){ctx.beginPath();ctx.moveTo(0,y);ctx.lineTo(w,y);ctx.stroke()}}
function relativeFft(values){if(!values.length)return[];const nums=values.map(Number).filter(Number.isFinite);if(!nums.length)return[];const min=Math.min(...nums),max=Math.max(...nums),span=max-min||1;return nums.map(v=>(v-min)/span)}
function drawSpectrumTo(canvas,ctx,values){const vals=relativeFft(values);drawGrid(ctx,canvas.width,canvas.height);if(!vals.length)return;ctx.strokeStyle='#37a9ff';ctx.shadowColor='#188eff';ctx.shadowBlur=6;ctx.lineWidth=2;ctx.beginPath();vals.forEach((v,i)=>{const x=i/(vals.length-1)*canvas.width;const py=canvas.height-(v*canvas.height);if(i===0)ctx.moveTo(x,py);else ctx.lineTo(x,py)});ctx.stroke();ctx.shadowBlur=0}
function drawSpectrum(values){drawSpectrumTo(spectrum,spectrumCtx,values);drawSpectrumTo(dashSpectrum,dashSpectrumCtx,values)}
function heat(v){const x=Math.max(0,Math.min(1,v));const hue=235-x*235;const light=18+x*42;return `hsl(${hue} 95% ${light}%)`}
function drawWaterfallTo(canvas,ctx,values){const vals=relativeFft(values);if(!vals.length)return;ctx.drawImage(canvas,0,-2);const y=canvas.height-2;vals.forEach((v,i)=>{const x=Math.floor(i/vals.length*canvas.width),x2=Math.ceil((i+1)/vals.length*canvas.width);ctx.fillStyle=heat(v);ctx.fillRect(x,y,Math.max(1,x2-x),2)})}
function drawWaterfall(values){drawWaterfallTo(wf,wfCtx,values);drawWaterfallTo(dashWf,dashWfCtx,values)}
function rfMHz(v){const m=String(v||'').match(/(-?\d+(?:\.\d+)?)/);return m?Number(m[1]):NaN}
function waterfallSpanLabel(d){const lo=rfMHz(d.lower_frequency),hi=rfMHz(d.upper_frequency);if(!Number.isFinite(lo)||!Number.isFinite(hi)||hi<=lo)return '—';const mhz=hi-lo;return mhz<1?`${Math.round(mhz*1000)} kHz`:`${mhz.toFixed(mhz<10?2:1)} MHz`}
async function readWfStatus(){try{const d=await api('/api/waterfall/status');text('#lowerFreq',formatDisplayFrequency(d.lower_frequency));text('#upperFreq',formatDisplayFrequency(d.upper_frequency));text('#centerFreq',formatDisplayFrequency(d.center_frequency));text('#markerFreq',formatDisplayFrequency(d.marker_frequency));text('#markerFreq2',formatDisplayFrequency(d.marker_frequency));text('#wfMod',d.modulation);text('#wfSpan',waterfallSpanLabel(d));text('#dashLowerFreq',formatDisplayFrequency(d.lower_frequency));text('#dashUpperFreq',formatDisplayFrequency(d.upper_frequency));text('#dashCenterFreq',formatDisplayFrequency(d.center_frequency));text('#dashMarkerFreq',formatDisplayFrequency(d.marker_frequency));text('#dashWfMod',d.modulation);return d}catch(e){log(`GST: ${e.message}`)}}
$('#waterfallStatus').onclick=readWfStatus;
async function pollWaterfall(){if(!waterfallRunning)return;try{const d=await api('/api/waterfall/latest');if(d.last_error){text('#wfState',`WAITING • ${d.last_error}`);text('#dashWfState','WAITING')};const line=d.latest;if(line&&line.received_at!==lastWfTimestamp){lastWfTimestamp=line.received_at;drawSpectrum(line.values||[]);drawWaterfall(line.values||[]);text('#fftCount',`${line.count} relative`);text('#wfState',`LIVE • ${d.received??''} frames`);text('#dashWfState',`LIVE • ${d.received??''}`)}}catch(e){text('#wfState','ERROR');text('#dashWfState','ERROR');log(`Waterfall: ${e.message}`)}}
async function startWaterfall(){try{const d=await control('/api/waterfall/start',{});waterfallRunning=true;text('#wfState','STARTING');text('#dashWfState','STARTING');await readWfStatus();clearInterval(waterfallTimer);waterfallTimer=setInterval(pollWaterfall,180);log('Physical SDS200 entered Waterfall mode; live GWF polling started');if(d.last_error)log(`Waterfall: ${d.last_error}`)}catch(e){text('#dashWfState','ERROR');log(e.message)}}
async function stopWaterfall(){try{const d=await control('/api/waterfall/stop',{});waterfallRunning=false;clearInterval(waterfallTimer);text('#wfState','STOPPED');text('#dashWfState','STOPPED');log(d.scan_restore?'Waterfall stopped; scanner returned to Scan mode':`Waterfall stopped; ${d.warning||'scan restore not confirmed'}`)}catch(e){log(e.message)}}
$('#waterfallStart').onclick=startWaterfall;$('#dashWaterfallStart').onclick=startWaterfall;$('#waterfallStop').onclick=stopWaterfall;$('#dashWaterfallStop').onclick=stopWaterfall;


function analysisOutput(mode){return {SYSTEM_STATUS:'#systemAnalysis',CURRENT_ACTIVITY:'#activityAnalysis',LCN_MONITOR:'#lcnAnalysis',RF_POWER_PLOT:'#powerAnalysis'}[mode]}
$$('[data-analysis]').forEach(b=>b.onclick=async()=>{const mode=b.dataset.analysis;try{const d=await control('/api/analysis/start',{mode,site_index:Number($(`#${b.dataset.siteSource}`).value)});$(analysisOutput(mode)).textContent=JSON.stringify(d,null,2);log(`${mode} started`)}catch(e){log(e.message)}});
$$('[data-pause]').forEach(b=>b.onclick=async()=>{const mode=b.dataset.pause;try{const d=await control('/api/analysis/pause',{mode});$(analysisOutput(mode)).textContent=JSON.stringify(d,null,2);log(`${mode} pause/resume sent`)}catch(e){log(e.message)}});
$('#rfPlot').onclick=async()=>{try{const d=await control('/api/analysis/start',{mode:'RF_POWER_PLOT',frequency:Number($('#rfFreq').value),modulation:$('#rfMod').value,sampling_rate:Number($('#rfRate').value)});$('#powerAnalysis').textContent=JSON.stringify(d,null,2);log('RF power plot started')}catch(e){log(e.message)}};

let audioCtx=null,audioSource=null,audioAnalyser=null,audioMeterFrame=null;
function meterOff(){cancelAnimationFrame(audioMeterFrame);audioMeterFrame=null;$('#audioMeter').classList.remove('active');$('#dashAudioMeter').classList.remove('active');$$('#audioMeter i, #dashAudioMeter i').forEach(b=>b.style.height='20%')}
function meterFromRealAudio(a){
  try{
    if(!audioCtx)audioCtx=new (window.AudioContext||window.webkitAudioContext)();
    if(!audioSource){audioSource=audioCtx.createMediaElementSource(a);audioAnalyser=audioCtx.createAnalyser();audioAnalyser.fftSize=256;audioSource.connect(audioAnalyser);audioAnalyser.connect(audioCtx.destination)}
    audioCtx.resume();$('#audioMeter').classList.add('active');$('#dashAudioMeter').classList.add('active');
    const data=new Uint8Array(audioAnalyser.fftSize);
    const tick=()=>{audioAnalyser.getByteTimeDomainData(data);let sum=0;for(const v of data){const x=(v-128)/128;sum+=x*x}const rms=Math.sqrt(sum/data.length);const level=Math.max(0,Math.min(1,rms*6));const bars=$$('#audioMeter i');bars.forEach((b,i)=>{const threshold=(i+1)/bars.length;b.style.height=`${20+(level>=threshold?70:0)}%`});const dbars=$$('#dashAudioMeter i');dbars.forEach((b,i)=>{const threshold=(i+1)/dbars.length;b.style.height=`${18+(level>=threshold?72:0)}%`});audioMeterFrame=requestAnimationFrame(tick)};
    cancelAnimationFrame(audioMeterFrame);tick();
  }catch(e){meterOff();log(`Audio meter unavailable: ${e.message}`)}
}
let audioStarting=false;
async function waitForRealRtp(initial){
  let d=initial;
  for(let i=0;i<8;i++){
    if((d?.session?.packets||0)>0)return d;
    await new Promise(r=>setTimeout(r,500));
    d=await api('/api/audio/status');
    if(d.state==='ERROR')throw new Error(`${d.stage||'RTSP'}: ${d.error||'audio start failed'}`);
  }
  return d;
}
async function attachLiveAudio(){
  const a=$('#audio');
  a.src=`/api/audio/live.wav?ts=${Date.now()}`;
  try{await a.play();meterFromRealAudio(a);log('Browser attached to confirmed RTP audio','INFO','AUDIO')}
  catch(e){meterOff();log(`Browser audio playback: ${e.message}`,'ERROR','AUDIO');throw e}
}
async function startLiveAudio(){
  if(audioStarting)return;
  audioStarting=true;$('#audioConnect').disabled=true;$('#dashListen').disabled=true;
  try{
    log('Listen Live requested; opening the single RTSP session','INFO','AUDIO');
    let d=await control('/api/audio/start',{});
    d=await waitForRealRtp(d);
    if((d?.session?.packets||0)<=0){log('RTSP PLAY succeeded but no RTP packets have arrived; browser source not attached','WARN','RTP');await audioStatus();return}
    await attachLiveAudio();await audioStatus();
  }catch(e){meterOff();log(`Audio start failed: ${e.message}`,'ERROR','AUDIO');await audioStatus()}
  finally{audioStarting=false;$('#audioConnect').disabled=false;$('#dashListen').disabled=false}
}
async function stopLiveAudio(){
  const a=$('#audio');a.pause();a.removeAttribute('src');a.load();meterOff();
  try{await control('/api/audio/stop',{});log('Remote audio stopped; RTSP TEARDOWN requested','INFO','AUDIO')}catch(e){log(`Audio stop: ${e.message}`,'ERROR','AUDIO')}
  await audioStatus();
}
$('#audioConnect').onclick=startLiveAudio;$('#dashListen').onclick=startLiveAudio;$('#audioStop').onclick=stopLiveAudio;$('#dashAudioStop').onclick=stopLiveAudio;
$('#dashMute').onclick=()=>{const a=$('#audio');a.muted=!a.muted;$('#dashMute').textContent=a.muted?'UNMUTE':'MUTE';log(a.muted?'Browser audio muted':'Browser audio unmuted','INFO','AUDIO')};
$('#audio').addEventListener('error',()=>{const a=$('#audio');if(!a.getAttribute('src'))return;const code=a.error?.code||0;log(`Browser audio element error ${code}`,'ERROR','AUDIO');meterOff();audioStatus()});

$('#recordOn').onclick=()=>control('/api/control/record',{enabled:true}).then(()=>{log('Scanner SD recording started');refresh()}).catch(e=>log(e.message));
$('#recordOff').onclick=()=>control('/api/control/record',{enabled:false}).then(()=>{log('Scanner SD recording stopped');refresh()}).catch(e=>log(e.message));
$('#dashRecordOn').onclick=()=>$('#recordOn').click();$('#dashRecordOff').onclick=()=>$('#recordOff').click();
async function remoteRecordState(){try{const d=await api('/api/recording/remote');text('#remoteRecordState',d.recording?`Recording • ${Math.floor(d.elapsed_s)}s • ${humanBytes(d.bytes)}`:(d.filename?`Stopped • ${d.filename}`:'Not recording'));text('#dashRemoteRecording',d.recording?`REC ${Math.floor(d.elapsed_s)}s`:(d.filename?'Stopped':'OFF'));const a=$('#remoteRecordLink');if(d.url){a.hidden=false;a.href=d.url}else a.hidden=true}catch(e){log(e.message)}}
$('#remoteRecordOn').onclick=async()=>{try{await control('/api/recording/remote/start',{});log('Remote audio recording started');remoteRecordState()}catch(e){log(e.message)}};
$('#remoteRecordOff').onclick=async()=>{try{await control('/api/recording/remote/stop',{});log('Remote audio recording stopped');remoteRecordState()}catch(e){log(e.message)}};
$('#dashRemoteRecordOn').onclick=()=>$('#remoteRecordOn').click();$('#dashRemoteRecordOff').onclick=()=>$('#remoteRecordOff').click();
function humanBytes(n){if(!n)return'0 B';if(n<1024)return`${n} B`;if(n<1048576)return`${(n/1024).toFixed(1)} KB`;return`${(n/1048576).toFixed(1)} MB`}
async function audioStatus(){
  try{
    const d=await api('/api/audio/status');const packets=d.session?.packets||0;const running=!!d.session?.running;const state=d.state||'NOT STARTED';
    const label=running?`● AUDIO STREAMING (${packets} RTP)`:state;
    text('#rtspState',label);text('#rtspDiag',running?'STREAMING':state);text('#rtspUrl',d.error?`${d.url||'RTSP'} · ${d.error}`:(d.url||'Not contacted'));text('#rtspHost',d.url||'Not contacted');text('#rtspLocalIp',d.local_ip||'—');text('#rtspPort',String(d.tcp_port||554));
    let msg='Audio has not been started. No background RTSP port probe is performed.';
    if(state==='STOPPED')msg='Audio stopped cleanly. Listen Live will create the next RTSP session.';
    if(running)msg=packets>0?`Receiving real RTP audio packets: ${packets}`:'RTSP PLAY succeeded; waiting for the first RTP packet. Browser audio has not been attached yet.';
    if(d.error)msg=`${d.stage||'RTSP'}: ${d.error}`;
    if(d.classification==='scanner-refused-port')msg='The Listen Live RTSP connection was refused by the scanner on TCP 554. No follow-up port probe will be made.';
    text('#audioError',msg);$('#rtspState').style.color=running?'#61f298':(d.error?'#ff8794':'');
    text('#dashAudioState',running?'AUDIO STREAMING':state);text('#dashAudioPackets',running?`${packets} RTP packets`:(d.classification||d.stage||'No RTSP session'));text('#dashRtsp',running?'STREAMING':state);
    return d;
  }catch(e){text('#dashAudioState','RTSP ERROR');log(e.message,'ERROR','AUDIO')}
}

$('#token').value=localStorage.sdsToken||'';$('#token').onchange=e=>localStorage.sdsToken=e.target.value;
async function acquire(){try{const d=await api('/api/lease/acquire',{method:'POST',headers:headers(true),body:JSON.stringify({client_id:clientId})});lease=!!d.acquired;text('#leaseState',lease?`CONTROL ACTIVE • lease ${d.expires_in}s`:`BUSY • another operator has control`);text('#dashLease',lease?'THIS BROWSER':'VIEW ONLY');if(lease)log('Operator control acquired')}catch(e){lease=false;text('#leaseState',`VIEW ONLY • ${e.message}`)}}
$('#acquireLease').onclick=acquire;$('#releaseLease').onclick=async()=>{try{await api('/api/lease/release',{method:'POST',headers:headers(true),body:JSON.stringify({client_id:clientId})})}catch{}lease=false;text('#leaseState','View only');text('#dashLease','VIEW ONLY');log('Operator control released')};setInterval(()=>{if(lease)acquire()},Math.max(10000,(config.lease_ttl||60)*500));
function updateDashSoftKeys(lines){const raw=[...(lines||[])].reverse().map(x=>String(x.text||'').trim()).find(Boolean)||'';const parts=raw.split(/\s{2,}/).map(x=>x.trim()).filter(Boolean);const labels=parts.length>=3?parts.slice(-3):['SOFT 1','SOFT 2','SOFT 3'];text('#dashSoftA',labels[0]);text('#dashSoftB',labels[1]);text('#dashSoftC',labels[2])}
function renderDisplay(d){
  const box=$('#remoteDisplay');if(!box||!d)return d;box.innerHTML='';box.dataset.form=d.display_form||'';
  for(const line of (d.lines||[])){const div=document.createElement('div');div.className='lcd-line';div.classList.add(line.font==='large'?'large':'small');if(String(line.mode||'').includes('*'))div.classList.add('reverse');if(String(line.mode||'').includes('_'))div.classList.add('underline');div.textContent=(line.text??'')||' ';box.appendChild(div)}
  if(!(d.lines||[]).length)box.innerHTML='<div class="empty">No STS display lines returned.</div>';$('#displayRaw').textContent=JSON.stringify(d,null,2);updateDashSoftKeys(d.lines||[]);return d;
}
async function readDisplay(){try{return renderDisplay(await api('/api/display'))}catch(e){log(`STS: ${e.message}`,'ERROR','STS')}}
$('#refreshDisplay').onclick=readDisplay;$('#readDisplay').onclick=readDisplay;
setInterval(()=>{if(activePage===2)readDisplay()},1200);
async function diagnostics(){try{const d=await api('/api/diagnostics');text('#udpDiag',d.scanner_online?'CONNECTED':'OFFLINE');text('#rtspDiag',d.audio?.session?.running?'STREAMING':(d.audio?.state||'NOT STARTED'));text('#diagBadge',d.scanner_online?'ALL SYSTEMS':'CHECK SYSTEM');text('#appVersion',`Web ${config.version||''}`);text('#dashUdp',d.scanner_online?'CONNECTED':'OFFLINE');text('#dashRtsp',d.audio?.session?.running?'STREAMING':(d.audio?.state||'NOT STARTED'));text('#dashEvent',d.scanner_online?`Real SDS200/SDS200E • ${config.host||''} • ${config.version||''}`:'Scanner connection check failed');return d}catch(e){text('#dashUdp','ERROR');log(e.message,'ERROR','SYSTEM')}}

async function discoveryStatus(){try{const d=await api('/api/discovery/status');text('#discoveryState',d.discovering?'SEARCHING…':(d.message||'Idle'));if(d.selected){$('#manualIp').value=d.selected;config.host=d.selected;text('#scannerHost',`${d.selected}:${config.udp_port||50536}`);if(d.selected!==lastSelectedHost){lastSelectedHost=d.selected;log(`SDS200 selected at ${d.selected}`);setTimeout(async()=>{await acquire();await refresh(true);await Promise.all([loadFqk(),loadFavorites(),readWfStatus(),audioStatus(),diagnostics()])},100)}}return d}catch(e){log(`Discovery: ${e.message}`)}}
$('#scanNetwork').onclick=async()=>{try{await api('/api/discovery/scan',{method:'POST',headers:headers(),body:'{}'});log('Network discovery started');setTimeout(discoveryStatus,250)}catch(e){log(e.message)}};
$('#dashReconnect').onclick=()=>$('#scanNetwork').click();
$('#useManualIp').onclick=async()=>{const host=$('#manualIp').value.trim();if(!host){log('Enter the SDS200 IPv4 address');return}try{const d=await api('/api/discovery/manual',{method:'POST',headers:headers(),body:JSON.stringify({host})});config=await api('/api/config');log(`Confirmed ${d.model||'SDS200-family scanner'} at ${d.selected}`);await discoveryStatus();await refresh(true)}catch(e){log(e.message)}};
setInterval(discoveryStatus,2000);

function formatLogEntry(e){const fields=e.fields&&Object.keys(e.fields).length?' '+JSON.stringify(e.fields):'';return `${e.time} ${String(e.level||'').padEnd(5)} ${String(e.component||'').padEnd(10)} ${e.message||''}${fields}`}
async function refreshPersistentLog(){
  try{const q=new URLSearchParams({limit:'600'});const lvl=$('#logLevel')?.value||'';const comp=$('#logComponent')?.value||'';const search=$('#logSearch')?.value||'';if(lvl&&lvl!==config.log_level){}if(comp)q.set('component',comp);if(search)q.set('q',search);const d=await api('/api/logs?'+q.toString());if($('#log'))$('#log').textContent=(d.entries||[]).map(formatLogEntry).join('\n');text('#logFile',d.file||'—');return d}catch(e){log(`Log refresh: ${e.message}`,'ERROR','APP')}}
$('#refreshLog').onclick=refreshPersistentLog;
$('#logComponent').onchange=refreshPersistentLog;$('#logSearch').oninput=()=>{clearTimeout(window.__logSearchTimer);window.__logSearchTimer=setTimeout(refreshPersistentLog,250)};
$('#logLevel').onchange=async e=>{try{const d=await control('/api/logging',{level:e.target.value});config.log_level=d.level;log(`Log level changed to ${d.level}`,'INFO','APP');await refreshPersistentLog()}catch(err){log(`Log level: ${err.message}`,'ERROR','APP')}};
$('#downloadLog').onclick=()=>{window.location.href='/api/logs/download'};
$('#openLogFolder').onclick=async()=>{try{const d=await control('/api/logs/open-folder',{});log(`Opened log folder: ${d.path}`,'INFO','APP')}catch(e){log(`Open log folder: ${e.message}`,'ERROR','APP')}};
$('#copyDiagnostics').onclick=async()=>{try{const d=await api('/api/diagnostics/bundle');const t=JSON.stringify(d,null,2);await navigator.clipboard.writeText(t);log('Diagnostic bundle copied to clipboard','INFO','APP')}catch(e){log(`Copy diagnostics: ${e.message}`,'ERROR','APP')}};
$('#clearLog').onclick=()=>{if($('#log'))$('#log').textContent=''};

(async()=>{try{config=await api('/api/config');capabilities=await api('/api/capabilities');if($('#logLevel'))$('#logLevel').value=config.log_level||'INFO';text('#scannerHost',config.host?`${config.host}:${config.udp_port}`:'Not selected');text('#appVersion',`Web ${config.version}`);if(config.host)log(`Configured scanner target ${config.host}:${config.udp_port}`);else log('No saved scanner target; automatic discovery starting');if(config.real_radio_only)log('REAL RADIO ONLY build — no simulation data or fallback');await discoveryStatus();config=await api('/api/config');if(config.host)await acquire();await refresh();await Promise.all([loadFqk(),loadFavorites(),memoryHome(),readWfStatus(),audioStatus(),diagnostics(),remoteRecordState(),readDisplay(),refreshPersistentLog()]);renderFqk();renderDashFqk();setInterval(refresh,1500);setInterval(remoteRecordState,2000);setInterval(diagnostics,7000);setInterval(audioStatus,3000)}catch(e){log(e.message)}})();

// ---- Service types (SVC) ----
let svcItems=[];
async function loadSvc(){try{const d=await api('/api/service-types');svcItems=d.items;renderSvc()}catch(e){$('#svcGrid').innerHTML='';const n=document.createElement('span');n.className='notice';n.textContent=`SVC read failed: ${e.message}`;$('#svcGrid').append(n)}}
function renderSvc(){const g=$('#svcGrid');g.innerHTML='';svcItems.filter(i=>i.name).forEach(i=>{const b=document.createElement('button');b.textContent=i.name;b.className=i.enabled?'on':'off';b.title=i.enabled?'Scanning — tap to disable':'Not scanned — tap to enable';b.onclick=()=>toggleSvc(i.slot);g.append(b)})}
async function toggleSvc(slot){const states=svcItems.map(i=>i.enabled?1:0);states[slot]=states[slot]?0:1;try{await control('/api/service-types',{states});log(`Service type ${svcItems[slot].name} ${states[slot]?'enabled':'disabled'}`,'INFO','CONTROL')}catch(e){log(`Service type write: ${e.message}`,'WARN','CONTROL')}await loadSvc()}
if($('#refreshSvc'))$('#refreshSvc').onclick=loadSvc;
// ---- Clock (DTM) ----
async function readClock(){try{const d=await api('/api/clock');const off=Math.abs(d.offset_s)<60?'in sync':`${d.offset_s>0?'+':''}${Math.round(d.offset_s/60)} min vs this computer`;text('#clockState',`Scanner ${d.scanner_time} (${off})${d.rtc_ok?'':' — RTC not OK'}`)}catch(e){text('#clockState',`DTM read failed: ${e.message}`)}}
if($('#readClock'))$('#readClock').onclick=readClock;
if($('#syncClock'))$('#syncClock').onclick=async()=>{try{await control('/api/clock',{});log('Scanner clock synchronised','INFO','CONTROL')}catch(e){log(`Clock sync: ${e.message}`,'WARN','CONTROL')}readClock()};
setTimeout(()=>{loadSvc();readClock()},1500);

// ---- FUNC + key (reliable: server checks FUNC state and waits out popups) ----
$$('[data-func]').forEach(b=>b.onclick=async()=>{const label=b.dataset.funcLabel||b.textContent.trim();try{const d=await control('/api/control/func',{key:b.dataset.func});if(d.display)renderDisplay(d.display);log(`FUNC + ${b.dataset.func} (${label})`,'INFO','CONTROL');setTimeout(refresh,200)}catch(e){log(`${label}: ${e.message}`,'WARN','CONTROL')}});
// ---- Range (LCR) — the SDS200 has no Range key ----
$$('[data-range]').forEach(b=>b.onclick=async()=>{try{const cur=await api('/api/location');const v=prompt(`Scan range in miles (current ${cur.range}; location ${cur.latitude.toFixed(4)}, ${cur.longitude.toFixed(4)})`,String(cur.range));if(v===null)return;const r=Number(v);if(!Number.isFinite(r)||r<0||r>999){log('Range must be 0–999 miles','WARN','CONTROL');return}await control('/api/location',{range:r});const now=await api('/api/location');log(`Range set to ${now.range} miles`,'INFO','CONTROL')}catch(e){log(`Range: ${e.message}`,'WARN','CONTROL')}});
