'use strict';
/* ОлеговщинаСМС — клиент. Шифрование на устройстве: X25519 + HKDF + AES-256-GCM (встроенный WebCrypto). */
const $=(s,r=document)=>r.querySelector(s);
const esc=s=>String(s??'').replace(/[&<>"']/g,c=>({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[c]));
const te=new TextEncoder(),td=new TextDecoder(),SUB=crypto.subtle;
const rnd=n=>crypto.getRandomValues(new Uint8Array(n));
const coarse=matchMedia('(pointer:coarse)').matches;
function b64u(u){let s='';for(let i=0;i<u.length;i+=0x8000)s+=String.fromCharCode.apply(null,u.subarray(i,i+0x8000));return btoa(s).replace(/\+/g,'-').replace(/\//g,'_').replace(/=+$/,'')}
function ub64(s){s=s.replace(/-/g,'+').replace(/_/g,'/');while(s.length%4)s+='=';const b=atob(s),u=new Uint8Array(b.length);for(let i=0;i<b.length;i++)u[i]=b.charCodeAt(i);return u}
const cat=(...a)=>{const o=new Uint8Array(a.reduce((x,y)=>x+y.length,0));let p=0;for(const x of a){o.set(x,p);p+=x.length}return o};
const NICK_RE=/^[A-Za-z][A-Za-z0-9_]{3,31}$/;
const fmtId=id=>String(id).replace(/(\d{3})(\d{3})(\d{3})/,'$1 $2 $3');

/* ---------- ключ аккаунта: 20 случайных байт в base32 ---------- */
const B32='ABCDEFGHJKMNPQRSTVWXYZ0123456789';
function toB32(u){let bits=0,val=0,out='';for(const b of u){val=(val<<8)|b;bits+=8;while(bits>=5){out+=B32[(val>>>(bits-5))&31];bits-=5}}if(bits)out+=B32[(val<<(5-bits))&31];return out}
function fromB32(s){s=s.toUpperCase().replace(/[^A-Z0-9]/g,'').replace(/O/g,'0').replace(/[IL]/g,'1').replace(/U/g,'V');let bits=0,val=0;const out=[];for(const c of s){const i=B32.indexOf(c);if(i<0)return null;val=(val<<5)|i;bits+=5;if(bits>=8){out.push((val>>>(bits-8))&255);bits-=8}}return new Uint8Array(out)}
const fmtKey=k=>toB32(k).match(/.{1,4}/g).join('-');
async function hkdf(ikm,salt,info){const k=await SUB.importKey('raw',ikm,'HKDF',false,['deriveBits']);return new Uint8Array(await SUB.deriveBits({name:'HKDF',hash:'SHA-256',salt:te.encode(salt),info:te.encode(info)},k,256))}
async function aes(raw){return SUB.importKey('raw',raw,'AES-GCM',false,['encrypt','decrypt'])}
async function encB(key,pt,aad){const iv=rnd(12);const o={name:'AES-GCM',iv};if(aad)o.additionalData=aad;return cat(iv,new Uint8Array(await SUB.encrypt(o,key,pt)))}
async function decB(key,d,aad){const o={name:'AES-GCM',iv:d.subarray(0,12)};if(aad)o.additionalData=aad;return new Uint8Array(await SUB.decrypt(o,key,d.subarray(12)))}
const P8=Uint8Array.from([0x30,0x2e,2,1,0,0x30,5,6,3,0x2b,0x65,0x6e,4,0x22,4,0x20]);
async function importPriv(seed){const k=await SUB.importKey('pkcs8',cat(P8,seed),{name:'X25519'},true,['deriveBits']);const j=await SUB.exportKey('jwk',k);const priv=await SUB.importKey('pkcs8',cat(P8,seed),{name:'X25519'},false,['deriveBits']);return{priv,pub:j.x}}
async function keysFromAK(ak){return{auth:b64u(await hkdf(ak,'shepot/1','auth')),wrap:await aes(await hkdf(ak,'shepot/1','wrap'))}}

/* ---------- состояние ---------- */
const LS='shepot.v1';
let me=null;          // {id,name,token,ak}
let myPriv=null,myPub='';
const chats=new Map(); // peerId -> {peer,msgs[],more,unread,peerRead,loaded,typing}
const ckCache=new Map();
function saveLocal(){try{localStorage.setItem(LS,JSON.stringify(me))}catch(e){}}
function loadLocal(){try{return JSON.parse(localStorage.getItem(LS)||'null')}catch(e){return null}}
function knownKeys(){try{return JSON.parse(localStorage.getItem(LS+'.keys')||'{}')}catch(e){return{}}}
function rememberKey(id,pub){const k=knownKeys();if(!k[id]){k[id]=pub;try{localStorage.setItem(LS+'.keys',JSON.stringify(k))}catch(e){}}return k[id]===pub}

async function api(path,body){
  const r=await fetch('/api/'+path,{method:body?'POST':'GET',headers:Object.assign({'Content-Type':'application/json'},me&&me.token?{Authorization:'Bearer '+me.token}:{}),body:body?JSON.stringify(body):undefined});
  let j={};try{j=await r.json()}catch(e){}
  if(r.status===401&&me){logout(true);throw new Error('Сессия истекла, войдите заново')}
  if(r.status===423&&me&&me.token){banOut(j.error);throw new Error(j.error)}
  if(!r.ok)throw new Error(j.error||'Сервер недоступен');return j;
}

/* ---------- шифрование переписки ---------- */
async function convKey(peer){
  let k=ckCache.get(peer.id);if(k)return k;
  const pub=await SUB.importKey('raw',ub64(peer.pub),{name:'X25519'},false,[]);
  const shared=new Uint8Array(await SUB.deriveBits({name:'X25519',public:pub},myPriv,256));
  const [a,b]=[me.id,peer.id].sort();k=await aes(await hkdf(shared,a+'|'+b,'shepot/dm/1'));ckCache.set(peer.id,k);return k;
}
async function encrypt(peer,obj){return b64u(await encB(await convKey(peer),te.encode(JSON.stringify(obj)),te.encode(me.id+'>'+peer.id)))}
async function decrypt(m){
  if(m._p!==undefined)return m._p;const peerId=m.from===me.id?m.to:m.from;const c=chats.get(peerId);
  try{m._p=JSON.parse(td.decode(await decB(await convKey(c.peer),ub64(m.ct),te.encode(m.from+'>'+m.to))))}catch(e){m._p={t:'bad'}}return m._p;
}
async function safetyCode(peer){const [x,y]=[myPub,peer.pub].sort();const h=new Uint8Array(await SUB.digest('SHA-256',te.encode(x+y)));let s='';for(let i=0;i<10;i++)s+=String((h[i*2]<<8|h[i*2+1])%100000).padStart(5,'0')+(i%2?'\n':' ');return s.trim()}

/* ---------- значки ---------- */
const I={
 menu:'<svg viewBox="0 0 24 24"><path d="M4 7h16M4 12h16M4 17h16"/></svg>',back:'<svg viewBox="0 0 24 24"><path d="M15 5l-7 7 7 7"/></svg>',
 close:'<svg viewBox="0 0 24 24"><path d="M6 6l12 12M18 6 6 18"/></svg>',pen:'<svg viewBox="0 0 24 24"><path d="M4 20h4L19 9a2.8 2.8 0 0 0-4-4L4 16z"/><path d="m13.5 6.5 4 4"/></svg>',
 send:'<svg viewBox="0 0 24 24"><path d="M4 12 20 4l-4 16-4-7z"/><path d="m12 13 8-9"/></svg>',photo:'<svg viewBox="0 0 24 24"><rect x="3" y="5" width="18" height="14" rx="2"/><circle cx="9" cy="10" r="1.8"/><path d="m21 16-5-5-8 8"/></svg>',
 dots:'<svg viewBox="0 0 24 24"><circle cx="12" cy="5" r="1.2"/><circle cx="12" cy="12" r="1.2"/><circle cx="12" cy="19" r="1.2"/></svg>',
 check:'<svg viewBox="0 0 24 24"><path d="m5 12 4.5 4.5L19 7"/></svg>',checks:'<svg viewBox="0 0 24 24"><path d="M2 12.5l4 4.5L15 7"/><path d="M10.5 16l1.2 1.2L21.5 7"/></svg>',
 clock:'<svg viewBox="0 0 24 24"><circle cx="12" cy="12" r="8"/><path d="M12 8v4l3 2"/></svg>',alert:'<svg viewBox="0 0 24 24"><circle cx="12" cy="12" r="9"/><path d="M12 7.5v5.5M12 16.5v.5"/></svg>',
 user:'<svg viewBox="0 0 24 24"><circle cx="12" cy="8" r="4"/><path d="M4 21a8 8 0 0 1 16 0"/></svg>',copy:'<svg viewBox="0 0 24 24"><rect x="8" y="8" width="12" height="12" rx="2"/><path d="M16 8V5a1 1 0 0 0-1-1H5a1 1 0 0 0-1 1v10a1 1 0 0 0 1 1h3"/></svg>',
 trash:'<svg viewBox="0 0 24 24"><path d="M4 7h16M9 7V4h6v3M6 7l1 13h10l1-13"/></svg>',reply:'<svg viewBox="0 0 24 24"><path d="M10 8 4 13l6 5"/><path d="M4 13h10a6 6 0 0 1 6 6"/></svg>',
 key:'<svg viewBox="0 0 24 24"><circle cx="8" cy="15" r="4"/><path d="m11 12 9-9M17 6l3 3M14.5 8.5l2 2"/></svg>',lock:'<svg viewBox="0 0 24 24"><rect x="5" y="11" width="14" height="10" rx="2"/><path d="M8 11V8a4 4 0 0 1 8 0v3"/></svg>',
 exit:'<svg viewBox="0 0 24 24"><path d="M15 4h4v16h-4M10 8l-4 4 4 4M6 12h11"/></svg>',moon:'<svg viewBox="0 0 24 24"><path d="M20 14.5A8 8 0 1 1 9.5 4a6.5 6.5 0 0 0 10.5 10.5z"/></svg>',
 block:'<svg viewBox="0 0 24 24"><circle cx="12" cy="12" r="9"/><path d="m5.6 5.6 12.8 12.8"/></svg>',bookmark:'<svg viewBox="0 0 24 24"><path d="M6 3h12v18l-6-4-6 4z"/></svg>',
 search:'<svg viewBox="0 0 24 24"><circle cx="11" cy="11" r="6.5"/><path d="m20 20-4.2-4.2"/></svg>',down:'<svg viewBox="0 0 24 24"><path d="m6 9 6 6 6-6"/></svg>',
 bubble:'<svg viewBox="0 0 24 24"><path d="M5 5h14a2 2 0 0 1 2 2v8a2 2 0 0 1-2 2h-8l-5 4v-4H5a2 2 0 0 1-2-2V7a2 2 0 0 1 2-2z"/></svg>',
 bell:'<svg viewBox="0 0 24 24"><path d="M6 16V11a6 6 0 0 1 12 0v5l2 2H4z"/><path d="M10 20a2 2 0 0 0 4 0"/></svg>',
 heart:'<svg viewBox="0 0 24 24"><path d="M12 20.3s-7.6-4.6-8.9-9.6A4.9 4.9 0 0 1 12 6.6a4.9 4.9 0 0 1 8.9 4.1c-1.3 5-8.9 9.6-8.9 9.6z"/></svg>',
 cmt:'<svg viewBox="0 0 24 24"><path d="M20 11.5a8 8 0 0 1-11.6 7.1L4 20l1.4-4.2A8 8 0 1 1 20 11.5z"/></svg>',
 plus:'<svg viewBox="0 0 24 24"><path d="M12 5v14M5 12h14"/></svg>',
 hash:'<svg viewBox="0 0 24 24"><path d="M9 4 7 20M17 4l-2 16M4.5 9h16M3.5 15h16"/></svg>',
 shield:'<svg viewBox="0 0 24 24"><path d="M12 3 4 6v6c0 4.5 3.4 8.3 8 9 4.6-.7 8-4.5 8-9V6z"/><path d="m9 12 2 2 4-4"/></svg>',
};
function toast(t){const el=$('#toast');el.textContent=t;el.classList.add('on');clearTimeout(toast.t);toast.t=setTimeout(()=>el.classList.remove('on'),2600)}
async function copy(t,msg='Скопировано'){try{await navigator.clipboard.writeText(t);toast(msg)}catch(e){const a=document.createElement('textarea');a.value=t;a.style.position='fixed';a.style.opacity='0';document.body.append(a);a.select();try{document.execCommand('copy');toast(msg)}catch(_){toast('Скопируйте вручную')}a.remove()}}
function hashN(s,n){let h=0;for(let i=0;i<s.length;i++)h=(h*31+s.charCodeAt(i))|0;return Math.abs(h)%n}
const fmtT=ms=>{const d=new Date(ms);return String(d.getHours()).padStart(2,'0')+':'+String(d.getMinutes()).padStart(2,'0')};
function fmtList(ms){if(!ms)return'';const d=new Date(ms),n=new Date();if(d.toDateString()===n.toDateString())return fmtT(ms);if((n-d)/864e5<6)return['вс','пн','вт','ср','чт','пт','сб'][d.getDay()];return d.toLocaleDateString('ru-RU',{day:'2-digit',month:'2-digit',year:'2-digit'})}
function dayLabel(ms){const d=new Date(ms),n=new Date();if(d.toDateString()===n.toDateString())return'Сегодня';const y=new Date(n);y.setDate(n.getDate()-1);if(d.toDateString()===y.toDateString())return'Вчера';return d.toLocaleDateString('ru-RU',{day:'numeric',month:'long',year:d.getFullYear()===n.getFullYear()?undefined:'numeric'})}
function linkify(s){return esc(s).replace(/(https?:\/\/[^\s<]+[^\s<.,;:!?)\]'"])/g,'<a href="$1" target="_blank" rel="noopener noreferrer">$1</a>')}
const isSelf=c=>c.peer.id===me.id;
const title=c=>isSelf(c)?'Избранное':(c.peer.name||fmtId(c.peer.id));
function avatar(c,cls=''){if(isSelf(c))return`<div class="av cs ${cls}">${I.bookmark}</div>`;const n=title(c);return`<div class="av c${hashN(c.peer.id,7)} ${cls}">${esc(([...n.trim()][0]||'?').toUpperCase())}${c.peer.online?'<span class="on-dot"></span>':''}</div>`}

/* ---------- навигация (кнопка «назад» телефона) ---------- */
const layers=[];let popWait=null;
function pushLayer(l){layers.push(l);l.open();try{history.pushState({d:layers.length},'')}catch(e){}}
function back(n=1){return new Promise(r=>{n=Math.min(n,layers.length);if(!n)return r();popWait=r;history.go(-n)})}
addEventListener('popstate',e=>{const d=(e.state&&e.state.d)||0;while(layers.length>d)layers.pop().close();if(popWait){const f=popWait;popWait=null;f()}});
function sheet(html,onMount){const el=$('#sheet');el.innerHTML='<div class="grab"></div>'+html;pushLayer({kind:'sheet',open(){el.classList.add('on');$('#scrim').classList.add('on')},close(){el.classList.remove('on');$('#scrim').classList.remove('on')}});if(onMount)onMount(el);return el}
function menuSheet(items,head=''){const list=items.filter(Boolean);return sheet(head+'<div class="sbody">'+list.map((it,i)=>`<button class="item ${it.danger?'danger':''}" data-i="${i}">${it.icon||''}<span class="grow">${esc(it.label)}${it.sub?`<small>${esc(it.sub)}</small>`:''}</span></button>`).join('')+'</div>',el=>el.querySelectorAll('[data-i]').forEach(b=>b.onclick=async()=>{await back();list[+b.dataset.i].go()}))}
function confirmSheet(t,txt,okL,danger){return new Promise(res=>{let done=false;sheet(`<div class="sh">${esc(t)}</div><div class="sp">${esc(txt)}</div><div class="acts"><button class="btn ghost" data-x="0">Отмена</button><button class="btn ${danger?'danger':''}" data-x="1">${esc(okL)}</button></div>`,el=>el.querySelectorAll('[data-x]').forEach(b=>b.onclick=()=>{done=true;back().then(()=>res(b.dataset.x==='1'))}))})}
$('#scrim').onclick=()=>back();
const Pages={};
function openPage(t,render,key,opts={}){
  const el=document.createElement('section');el.className='scr';el.innerHTML=`<header class="bar"><button class="ib pb" aria-label="Назад">${I.back}</button><div class="ttl"><b>${esc(t)}</b></div></header><div class="scroll pg"></div>${opts.footer?'<footer class="pfoot"></footer>':''}`;
  $('#app').insertBefore(el,$('#scrim'));const body=el.querySelector('.pg'),foot=el.querySelector('.pfoot');const rr=()=>render(body,foot);if(key)Pages[key]=rr;
  pushLayer({kind:'page',open(){requestAnimationFrame(()=>requestAnimationFrame(()=>el.classList.add('on')))},close(){el.classList.remove('on');if(key)delete Pages[key];if(opts.onClose)opts.onClose();setTimeout(()=>el.remove(),320)}});
  el.querySelector('.pb').onclick=()=>back();rr();
}
function theme(){return localStorage.getItem(LS+'.theme')||'system'}
function applyTheme(){const t=theme(),r=document.documentElement;if(t==='system')r.removeAttribute('data-theme');else r.setAttribute('data-theme',t)}
const isDark=()=>theme()==='dark'||(theme()==='system'&&matchMedia('(prefers-color-scheme: dark)').matches);

/* ---------- вход и регистрация ---------- */
function show(id){['boot','onb','home'].forEach(x=>$('#'+x).hidden=x!==id)}
let srvInfo={name:'ОлеговщинаСМС',invite:false};
function banOut(msg){try{sessionStorage.setItem(LS+'.msg',msg||'Ваш аккаунт заблокирован администратором')}catch(e){}logout(true)}
function welcome(){
  show('onb');const box=$('#onbBox');
  try{const m=sessionStorage.getItem(LS+'.msg');if(m){sessionStorage.removeItem(LS+'.msg');setTimeout(()=>toast(m),300)}}catch(e){}
  box.innerHTML=`<div class="mark">${I.bubble}</div><h1>${esc(srvInfo.name)}</h1><p class="lead">Простой мессенджер со сквозным шифрованием. Без почты и телефона: ваш аккаунт — это ключ.</p>
    <input class="field" id="rName" maxlength="40" placeholder="Ваше имя" autocomplete="nickname">
    ${srvInfo.invite?'<input class="field" id="rInv" placeholder="Код приглашения" autocomplete="off">':''}
    <button class="btn wide" id="rGo">Создать аккаунт</button>
    <div class="or"><span>или</span></div>
    <textarea class="field" id="lKey" rows="2" placeholder="Ключ аккаунта XXXX-XXXX-…" autocomplete="off" autocapitalize="characters" spellcheck="false"></textarea>
    <button class="btn ghost wide" id="lGo">Войти по ключу</button>`;
  $('#rGo').onclick=register;$('#lGo').onclick=()=>login($('#lKey').value);
}
async function register(){
  const name=$('#rName').value.trim();if(!name)return toast('Как вас называть?');
  const b=$('#rGo');b.disabled=true;b.textContent='Создаю ключи…';
  try{
    const ak=rnd(20),seed=rnd(32);const{auth,wrap}=await keysFromAK(ak);const k=await importPriv(seed);
    const encPriv=b64u(await encB(wrap,seed));
    const r=await api('register',{auth,pub:k.pub,encPriv,name,invite:$('#rInv')?$('#rInv').value.trim():''});
    me={id:r.id,name:r.name,token:r.token,ak:fmtKey(ak)};myPriv=k.priv;myPub=k.pub;saveLocal();
    showKey(true);
  }catch(e){toast(e.message);b.disabled=false;b.textContent='Создать аккаунт'}
}
function showKey(first){
  show('onb');const box=$('#onbBox');
  box.innerHTML=`<div class="mark">${I.key}</div><h1>${first?'Ваш аккаунт готов':'Ключ аккаунта'}</h1>
    <div class="idcard"><span class="lbl">Ваш ID — по нему вас найдут</span><b>${fmtId(me.id)}</b></div>
    <span class="lbl">Ключ аккаунта — для входа</span><div class="keybox" id="kb">${esc(me.ak)}</div>
    <button class="btn ghost wide" id="kc">${I.copy} Скопировать ключ</button>
    <div class="warnbox">Сохраните ключ: запишите на бумаге или в менеджер паролей. Это единственный способ войти на другом устройстве или после выхода. Восстановить его нельзя, даже владельцу сервера. Никому не показывайте ключ.</div>
    <label class="agree"><input type="checkbox" id="ka"> Я сохранил(а) ключ</label>
    <button class="btn wide" id="kg" disabled>Продолжить</button>`;
  $('#kc').onclick=()=>copy(me.ak,'Ключ скопирован');$('#ka').onchange=e=>$('#kg').disabled=!e.target.checked;$('#kg').onclick=()=>enter();
}
async function login(raw){
  const ak=fromB32(raw||'');if(!ak||ak.length!==20)return toast('Ключ состоит из 32 символов: проверьте, что скопирован целиком');
  const b=$('#lGo');b.disabled=true;b.textContent='Вхожу…';
  try{const{auth,wrap}=await keysFromAK(ak);const r=await api('login',{auth});const seed=await decB(wrap,ub64(r.encPriv));const k=await importPriv(seed);
    me={id:r.id,name:r.name,token:r.token,ak:fmtKey(ak)};myPriv=k.priv;myPub=k.pub;saveLocal();enter();
  }catch(e){toast(e.message);b.disabled=false;b.textContent='Войти по ключу'}
}
async function resume(){
  const s=loadLocal();if(!s||!s.token||!s.ak)return welcome();me=s;
  try{const ak=fromB32(s.ak);const{wrap}=await keysFromAK(ak);const r=await api('me');const k=await importPriv(await decB(wrap,ub64(r.encPriv)));myPriv=k.priv;myPub=k.pub;me.name=r.name;blocked=new Set(r.blocked||[]);enter()}
  catch(e){if(me)$('#bootMsg').textContent=e.message+'. Обновите страницу.';else welcome()}
}
async function logout(silent){
  if(!silent&&!await confirmSheet('Выйти?','Чтобы войти снова, понадобится ключ аккаунта. Без него вернуть аккаунт нельзя.','Выйти',true))return;
  try{if(!silent)await api('logout',{})}catch(e){}
  try{localStorage.removeItem(LS)}catch(e){}if(es)es.close();location.replace('/');
}

/* ---------- главный экран ---------- */
let blocked=new Set(),es=null,cur=null;
async function enter(){
  show('home');$('#meName').textContent=me.name;setTab(tab);renderList();
  try{const r=await api('me');me.admin=!!r.admin;me.nick=r.nick||'';me.name=r.name;saveLocal();blocked=new Set(r.blocked||[]);if(me.admin)modCount()}catch(e){}
  await loadChats();connectEvents();
  handleHash();
}
async function loadChats(){
  try{const list=await api('chats');for(const x of list){const c=chat(x.peer);c.unread=x.unread;c.peerRead=x.peerRead;if(!c.msgs.some(m=>m.id===x.last.id)&&!c.loaded)c.msgs=[x.last];await decrypt(c.msgs[c.msgs.length-1])}renderList()}catch(e){toast(e.message)}
}
function chat(peer){let c=chats.get(peer.id);if(!c){c={peer,msgs:[],unread:0,peerRead:0,loaded:false,more:false};chats.set(peer.id,c)}else Object.assign(c.peer,peer);
  if(peer.pub&&!rememberKey(peer.id,peer.pub))c.keyWarn=true;return c}
function connectEvents(){
  if(es)es.close();es=new EventSource('/api/events?token='+encodeURIComponent(me.token));
  es.addEventListener('hello',()=>{$('#net').textContent='В сети · ID '+fmtId(me.id);if(connectEvents.was)resync();connectEvents.was=true});
  es.onerror=()=>{$('#net').textContent='Нет связи с сервером…'};
  es.addEventListener('msg',async e=>{const m=JSON.parse(e.data);await incoming(m)});
  es.addEventListener('read',e=>{const d=JSON.parse(e.data);const c=chats.get(d.peer);if(c){c.peerRead=Math.max(c.peerRead,d.upto);refresh(c)}});
  es.addEventListener('myread',e=>{const d=JSON.parse(e.data);const c=chats.get(d.peer);if(c){c.unread=0;refresh(c)}});
  es.addEventListener('del',e=>{const d=JSON.parse(e.data);const c=chats.get(d.from===me.id?d.to:d.from);if(c){c.msgs=c.msgs.filter(m=>m.id!==d.id);refresh(c)}});
  es.addEventListener('typing',e=>{const d=JSON.parse(e.data);const c=chats.get(d.from);if(c){c.typing=Date.now()+5000;refresh(c);setTimeout(()=>refresh(c),5200)}});
  es.addEventListener('post',e=>{const d=JSON.parse(e.data);if(d.author!==me.id&&!blocked.has(d.author)&&feed.loaded){feed.fresh=true;$('#feedNew').hidden=tab!=='feed'}});
  es.addEventListener('pupd',e=>{const d=JSON.parse(e.data);const p=posts.get(d.id);if(p){p.likes=d.likes;if(d.by===me.id)p.liked=d.on;updatePost(p)}});
  es.addEventListener('banned',()=>banOut());
  es.addEventListener('modq',()=>{if(me.admin)modCount()});
  es.addEventListener('approved',e=>{const d=JSON.parse(e.data);const p=posts.get(d.id);if(p){p.pending=false;updatePost(p)}toast('Ваш пост прошёл проверку и опубликован')});
  es.addEventListener('pdel',e=>{const d=JSON.parse(e.data);dropPost(d.id)});
  es.addEventListener('cmt',e=>{const d=JSON.parse(e.data);const p=posts.get(d.post);if(p){p.cmts=d.n;updatePost(p)}if(postPage&&postPage.id===d.post&&!postPage.cmts.some(c=>c.id===d.cmt.id)&&!blocked.has(d.cmt.author.id)){postPage.cmts.push(d.cmt);renderCmts(true)}});
  es.addEventListener('cdel',e=>{const d=JSON.parse(e.data);const p=posts.get(d.post);if(p){p.cmts=d.n;updatePost(p)}if(postPage&&postPage.id===d.post){postPage.cmts=postPage.cmts.filter(c=>c.id!==d.id);renderCmts()}});
  es.addEventListener('user',e=>{const u=JSON.parse(e.data);if(u.id===me.id){me.nick=u.nick||'';saveLocal()}for(const p of posts.values())if(p.author.id===u.id)Object.assign(p.author,{name:u.name,nick:u.nick});const c=chats.get(u.id);if(c){Object.assign(c.peer,u);refresh(c)}});
}
async function resync(){if(feed.loaded)loadFeed();await loadChats();if(cur){const c=chats.get(cur);c.loaded=false;await loadMsgs(c);renderChat(true)}}
async function incoming(m){
  const peerId=m.from===me.id?m.to:m.from;let c=chats.get(peerId);
  if(!c){try{c=chat(await api('user?id='+peerId))}catch(e){return}}
  if(c.msgs.some(x=>x.id===m.id))return;
  c.msgs.push(m);c.msgs.sort((a,b)=>a.ts-b.ts);await decrypt(m);
  if(m.from!==me.id){c.typing=0;if(cur===peerId&&!document.hidden)markRead(c);else{c.unread++;notify(c,m)}}
  refresh(c);
}
function refresh(c){renderList();if(cur===c.peer.id)renderChat()}
function notify(c,m){if(localStorage.getItem(LS+'.notify')!=='1'||!document.hidden)return;try{if(Notification.permission==='granted'){const n=new Notification(title(c),{body:preview(m),tag:c.peer.id});n.onclick=()=>{focus();openChat(c.peer.id);n.close()}}}catch(e){}}
function preview(m){const p=m._p||{};if(p.t==='img')return'📷 Фото'+(p.text?' · '+p.text:'');if(p.t==='bad')return'🔒 Не удалось расшифровать';return(p.text||'').replace(/\s+/g,' ').slice(0,100)}
function renderList(){
  if($('#home').hidden)return;const q=($('#q').value||'').trim().toLowerCase();
  let list=[...chats.values()].filter(c=>c.msgs.length||isSelf(c)&&c.loaded).filter(c=>!blocked.has(c.peer.id));
  if(q)list=list.filter(c=>title(c).toLowerCase().includes(q)||c.peer.id.includes(q.replace(/\s/g,'')));
  list.sort((a,b)=>(b.msgs.at(-1)?.ts||0)-(a.msgs.at(-1)?.ts||0));
  let n=0;list.forEach(c=>n+=c.unread);document.title=n?`(${n}) ${srvInfo.name}`:srvInfo.name;$('#tabCnt').hidden=!n;$('#tabCnt').textContent=n;
  $('#list').innerHTML=list.map(c=>{const m=c.msgs.at(-1);const mine=m&&m.from===me.id&&!isSelf(c);
    const ty=c.typing>Date.now();
    return`<div class="row" data-peer="${c.peer.id}">${avatar(c)}<div class="rb"><div class="r1"><b><span style="overflow:hidden;text-overflow:ellipsis">${esc(title(c))}</span></b><span class="tm">${mine?(c.peerRead>=m.ts?I.checks:I.check):''}${m?fmtList(m.ts):''}</span></div><div class="r2"><span class="pv">${ty?'<span class="typing">печатает…</span>':(mine?'<em>Вы: </em>':'')+esc(m?preview(m):'')}</span>${c.unread?`<span class="badge">${c.unread}</span>`:''}</div></div></div>`}).join('')
    ||`<div class="empty"><div class="glyph">${I.bubble}</div><h3>Пока нет чатов</h3><p>Найдите человека по ID или никнейму, или отправьте свой ID другу.</p><div class="myid">Ваш ID<b>${fmtId(me.id)}</b></div><div style="display:flex;gap:8px;flex-wrap:wrap;justify-content:center"><button class="btn" data-act="find">${I.search} Найти человека</button><button class="btn ghost" data-act="copyid">${I.copy} Мой ID</button></div></div>`;
}

/* ---------- чат ---------- */
let replyTo=null;
async function loadMsgs(c,older){
  const before=older&&c.msgs.length?c.msgs[0].ts:0;
  const r=await api(`messages?peer=${c.peer.id}${before?'&before='+before:''}`);
  const have=new Set(c.msgs.map(m=>m.id));const add=r.msgs.filter(m=>!have.has(m.id));
  if(!older&&!c.loaded){c.msgs=[...r.msgs,...c.msgs.filter(m=>m._local)];}else c.msgs=[...add,...c.msgs];
  c.msgs.sort((a,b)=>a.ts-b.ts);c.more=r.more;c.peerRead=Math.max(c.peerRead,r.peerRead||0);c.loaded=true;
  for(const m of c.msgs)await decrypt(m);
}
async function openChat(peerId){
  await back(layers.length);let c=chats.get(peerId);
  if(!c){try{c=chat(await api('user?id='+peerId))}catch(e){return toast(e.message)}}
  cur=peerId;replyTo=null;renderCtx();
  $('#cTtl b').textContent=title(c);$('#cAv').innerHTML=avatar(c,'sm');$('#inp').value=c.draft||'';autosize();
  pushLayer({kind:'chat',open(){$('#chat').classList.add('on')},close(){c.draft=$('#inp').value;cur=null;$('#chat').classList.remove('on');renderList()}});
  $('#msgs').innerHTML='<div class="empty"><div class="spin"></div></div>';renderHead(c);
  try{if(!c.loaded)await loadMsgs(c)}catch(e){toast(e.message)}
  renderChat(true);markRead(c);
}
function renderHead(c){
  $('#cTtl b').textContent=title(c);$('#cAv').innerHTML=avatar(c,'sm');
  const s=$('#cTtl small');const ty=c.typing>Date.now();
  s.textContent=ty?'печатает…':isSelf(c)?'Заметки для себя':c.peer.online?'в сети':c.peer.nick?'@'+c.peer.nick:'ID '+fmtId(c.peer.id);s.classList.toggle('typing',ty);
  $('#keyWarn').hidden=!c.keyWarn;
}
function bubble(m,prev,c){
  const out=m.from===me.id;const p=m._p||{};const cont=prev&&prev.from===m.from&&m.ts-prev.ts<300000;
  let body='';
  if(p.re){const r=c.msgs.find(x=>x.id===p.re);body+=`<div class="rq" data-jump="${esc(p.re)}"><b>${r?(r.from===me.id?'Вы':esc(title(c))):'Сообщение'}</b><span>${r?esc(preview(r)):'удалено'}</span></div>`}
  const tick=out&&!isSelf(c)?(m._fail?`<span class="fail">${I.alert}</span>`:m._local?I.clock:c.peerRead>=m.ts?I.checks:I.check):'';
  const meta=`<span class="meta">${fmtT(m.ts)}${tick}</span>`;
  if(p.t==='img'&&/^data:image\/(jpeg|png|webp);base64,[A-Za-z0-9+/=]+$/.test(p.img))body+=`<img class="media" src="${p.img}" alt="Фото" data-view>`;
  if(p.t==='bad')body+=`<div class="txt"><i class="del">🔒 Не удалось расшифровать</i>${meta}</div>`;
  else body+=`<div class="txt">${linkify(p.text||'')}${meta}</div>`;
  return`<div class="msg ${out?'out':'in'} ${cont?'':'gap'}" data-id="${esc(m.id)}"><div class="bub">${body}</div></div>`;
}
function renderChat(force){
  const c=chats.get(cur);if(!c)return;const box=$('#msgs');renderHead(c);
  const atBottom=box.scrollHeight-box.scrollTop-box.clientHeight<120;const prevH=box.scrollHeight,prevTop=box.scrollTop;
  let h=c.more?'<button class="more" data-more>Показать ранние сообщения</button>':'';let day='',prev=null;
  for(const m of c.msgs){const d=dayLabel(m.ts);if(d!==day){h+=`<div class="day"><span>${d}</span></div>`;day=d;prev=null}h+=bubble(m,prev,c);prev=m}
  box.innerHTML=c.msgs.length?h:`<div class="empty" style="margin:auto"><div class="glyph">${isSelf(c)?I.bookmark:I.lock}</div><h3>${isSelf(c)?'Избранное':'Начните переписку'}</h3><p>${isSelf(c)?'Заметки, которые видите только вы.':'Сообщения шифруются на вашем устройстве. Сервер хранит только шифротекст и не может его прочитать.'}</p></div>`;
  if(force||atBottom)box.scrollTop=box.scrollHeight;else if(box._older)box.scrollTop=prevTop+box.scrollHeight-prevH;else box.scrollTop=prevTop;box._older=false;
}
let readT;
function markRead(c){const last=c.msgs.filter(m=>m.from!==me.id&&!m._local).at(-1);c.unread=0;renderList();if(!last||isSelf(c))return;clearTimeout(readT);readT=setTimeout(()=>api('read',{peer:c.peer.id,upto:last.ts}).catch(()=>{}),400)}

async function sendPayload(c,payload){
  const tmp='tmp'+Math.random().toString(36).slice(2);const m={id:tmp,from:me.id,to:c.peer.id,ts:Date.now(),_local:1,_p:payload};c.msgs.push(m);refresh(c);
  try{const ct=await encrypt(c.peer,payload);const r=await api('send',{to:c.peer.id,ct});
    const i=c.msgs.indexOf(m);const real=Object.assign(r,{_p:payload});if(c.msgs.some(x=>x.id===r.id))c.msgs.splice(i,1);else c.msgs[i]=real;c.msgs.sort((a,b)=>a.ts-b.ts)}
  catch(e){m._fail=1;m._ct=null;toast(e.message)}
  refresh(c);
}
function doSend(){
  const c=chats.get(cur);const text=$('#inp').value.trim();if(!text)return;if(text.length>10000)return toast('Слишком длинное сообщение');
  const payload={t:'text',text};if(replyTo){payload.re=replyTo.id;replyTo=null;renderCtx()}
  $('#inp').value='';c.draft='';autosize();if(!coarse)$('#inp').focus();sendPayload(c,payload);setTimeout(()=>{const b=$('#msgs');b.scrollTop=b.scrollHeight},50);
}
function renderCtx(){const bar=$('#ctxbar');if(!replyTo){bar.hidden=true;return}bar.hidden=false;const c=chats.get(cur);
  bar.innerHTML=`${I.reply}<div class="pl"><b>Ответ: ${replyTo.from===me.id?'Вы':esc(title(c))}</b><span>${esc(preview(replyTo))}</span></div><button class="ib" id="ctxX" aria-label="Отменить">${I.close}</button>`;$('#ctxX').onclick=()=>{replyTo=null;renderCtx()}}
function autosize(){const t=$('#inp');t.style.height='auto';t.style.height=Math.min(t.scrollHeight,180)+'px'}
let typT=0;function typing(){if(cur&&cur!==me.id&&Date.now()-typT>4000){typT=Date.now();api('typing',{to:cur}).catch(()=>{})}}
function msgMenu(id){
  const c=chats.get(cur);const m=c.msgs.find(x=>x.id===id);if(!m)return;const p=m._p||{};
  menuSheet([
    {icon:I.reply,label:'Ответить',go:()=>{replyTo=m;renderCtx();$('#inp').focus()}},
    p.text&&{icon:I.copy,label:'Копировать текст',go:()=>copy(p.text)},
    m._fail&&{icon:I.send,label:'Отправить снова',go:()=>{c.msgs=c.msgs.filter(x=>x!==m);sendPayload(c,p)}},
    m.from===me.id&&!m._local&&{icon:I.trash,label:'Удалить у всех',danger:true,go:async()=>{if(await confirmSheet('Удалить сообщение?','Оно исчезнет у вас и у собеседника, а также с сервера.','Удалить',true))api('delete',{id:m.id}).catch(e=>toast(e.message))}},
  ],`<div class="preview">${esc(preview(m))}</div>`);
}
function compressImage(file,max=1280,limit=450000){
  return new Promise((res,rej)=>{const img=new Image(),url=URL.createObjectURL(file);
    img.onload=()=>{URL.revokeObjectURL(url);const cv=document.createElement('canvas'),x=cv.getContext('2d');let q=.82,d;
      do{const s=Math.min(1,max/Math.max(img.width,img.height));cv.width=Math.round(img.width*s);cv.height=Math.round(img.height*s);x.fillStyle='#fff';x.fillRect(0,0,cv.width,cv.height);x.drawImage(img,0,0,cv.width,cv.height);d=cv.toDataURL('image/jpeg',q);max=Math.round(max*.85);q=Math.max(.5,q-.06)}while(d.length>limit&&max>200);res(d)};
    img.onerror=()=>{URL.revokeObjectURL(url);rej(new Error('Не удалось открыть изображение'))};img.src=url});
}
$('#fImg').onchange=async e=>{const f=e.target.files[0];e.target.value='';if(!f||!cur)return;try{const img=await compressImage(f);const text=$('#inp').value.trim();$('#inp').value='';autosize();sendPayload(chats.get(cur),{t:'img',img,text})}catch(err){toast(err.message)}};

/* ---------- страницы ---------- */
function findPage(){
  openPage('Найти человека',body=>{
    body.innerHTML=`<div class="card"><div class="pad"><span class="lbl">ID или никнейм</span><div class="inrow"><input class="field" id="fid" placeholder="123 456 789 или @nickname" autocomplete="off" autocapitalize="off" spellcheck="false" enterkeyhint="search"><button class="btn" id="fgo" aria-label="Найти">${I.search}</button></div>
      <div id="fres"></div><span class="note">ID — это 9 цифр, никнейм начинается с @. Их человек видит в профиле приложения.</span></div></div>
      <div class="card"><div class="pad"><span class="lbl">Ваш ID</span><div class="idline"><b>${fmtId(me.id)}</b><button class="btn ghost" id="fcp">${I.copy} Копировать</button></div></div></div>`;
    const inp=body.querySelector('#fid');setTimeout(()=>inp.focus(),300);
    inp.oninput=()=>{if(/^[\d\s]+$/.test(inp.value)){const d=inp.value.replace(/\D/g,'').slice(0,9);inp.value=fmtId(d)}};
    const go=async()=>{const v=inp.value.trim();let q;
      if(/^[\d\s]+$/.test(v)){q=v.replace(/\D/g,'');if(q.length!==9)return toast('ID — это 9 цифр')}
      else{q=v.replace(/^@/,'');if(!NICK_RE.test(q))return toast('Никнейм: латинские буквы, цифры и _, от 4 символов')}
      try{const u=await api('user?id='+encodeURIComponent(q));const c=chat(u);body.querySelector('#fres').innerHTML=`<div class="row" id="fopen">${avatar(c,'sm')}<div class="rb"><div class="r1"><b>${esc(title(c))}</b></div><div class="r2"><span class="pv">${u.nick?'@'+esc(u.nick)+' · ':''}ID ${fmtId(u.id)}${u.online?' · в сети':''}</span></div></div></div>`;body.querySelector('#fopen').onclick=()=>openChat(u.id)}
      catch(e){body.querySelector('#fres').innerHTML=`<p class="note" style="color:var(--danger)">${esc(e.message)}</p>`}};
    body.querySelector('#fgo').onclick=go;inp.onkeydown=e=>{if(e.key==='Enter')go()};body.querySelector('#fcp').onclick=()=>copy(me.id,'ID скопирован');
  });
}
async function chatInfo(){
  const c=chats.get(cur);if(isSelf(c))return;const code=await safetyCode(c.peer);const bl=blocked.has(c.peer.id);
  openPage('Собеседник',body=>{
    body.innerHTML=`<div class="hero">${avatar(c,'xl')}<h2>${esc(title(c))}</h2><p>${c.peer.nick?'@'+esc(c.peer.nick)+' · ':''}ID ${fmtId(c.peer.id)}${c.peer.online?' · в сети':''}</p></div>
      ${c.keyWarn?'<div class="pad"><div class="warnbox">Ключ шифрования этого собеседника изменился с прошлого раза. Так бывает, только если сервер подменил ключ. Сверьте код безопасности.</div></div>':''}
      <div class="card"><div class="pad"><span class="lbl">Код безопасности</span><div class="keybox" style="white-space:pre-line;text-align:center">${code}</div><span class="note">Сравните этот код с кодом у собеседника, при встрече или по телефону. Если цифры совпадают, переписку не может прочитать никто, включая владельца сервера.</span></div></div>
      <div class="card"><button class="item" id="ciCopy">${I.copy}<span class="grow">Скопировать ID</span></button><button class="item" id="ciRep">${I.alert}<span class="grow">Пожаловаться<small>Сообщения зашифрованы, модератор их не видит. Жалоба помогает заблокировать нарушителя</small></span></button><button class="item ${bl?'':'danger'}" id="ciBl">${I.block}<span class="grow">${bl?'Разблокировать':'Заблокировать'}</span></button></div>`;
    body.querySelector('#ciRep').onclick=()=>reportSheet('user',c.peer.id);
    body.querySelector('#ciCopy').onclick=()=>copy(c.peer.id,'ID скопирован');
    body.querySelector('#ciBl').onclick=async()=>{const nb=!bl;if(nb&&!await confirmSheet('Заблокировать?','Этот человек не сможет вам писать.','Заблокировать',true))return;await api('block',{id:c.peer.id,block:nb});nb?blocked.add(c.peer.id):blocked.delete(c.peer.id);toast(nb?'Заблокирован':'Разблокирован');await back(layers.length)};
  });
}
function profilePage(){
  openPage('Профиль и настройки',body=>{
    const nOk='Notification'in window,nOn=localStorage.getItem(LS+'.notify')==='1';
    body.innerHTML=`<div class="hero"><div class="av c${hashN(me.id,7)} xl">${esc(([...me.name][0]||'?').toUpperCase())}</div><h2>${esc(me.name)}</h2>${me.nick?`<p>@${esc(me.nick)}</p>`:''}</div>
      <div class="card"><div class="pad"><span class="lbl">Никнейм</span><div class="inrow"><div class="field nickf"><span>@</span><input id="pNick" maxlength="32" placeholder="придумайте ник" autocomplete="off" autocapitalize="off" spellcheck="false"></div><button class="btn" id="pNickSave">Сохранить</button></div>
        <span class="note" id="pNickSt">По никнейму вас найдут так же, как по ID. Латинские буквы, цифры и _, от 4 символов. Занятый ник взять нельзя.</span>
        ${me.nick?`<button class="btn ghost" id="pLink">${I.copy} Копировать ссылку на меня</button>`:''}</div></div>
      <div class="card"><div class="pad"><span class="lbl">Ваш ID</span><div class="idline"><b>${fmtId(me.id)}</b><button class="btn ghost" id="pId">${I.copy} Копировать</button></div><span class="note">Отправьте ID другу, чтобы он смог вас найти.</span></div></div>
      <div class="card"><div class="pad"><span class="lbl">Имя</span><div class="inrow"><input class="field" id="pName" maxlength="40"><button class="btn" id="pSave">Сохранить</button></div></div></div>
      ${me.admin?`<div class="card"><button class="item" id="pAdm">${I.shield}<span class="grow">Админ-панель<small>Проверка постов, жалобы, пользователи, стоп-слова</small></span>${modN?`<span class="badge">${modN}</span>`:''}</button></div>`:''}
      <div class="card"><button class="item" id="pRules">${I.alert}<span class="grow">Правила</span></button></div>
      <div class="card"><button class="item" id="pKey">${I.key}<span class="grow">Ключ аккаунта<small>Нужен для входа на другом устройстве</small></span></button>
      <button class="item" id="pTheme">${I.moon}<span class="grow">Тёмная тема</span><span class="sw ${isDark()?'on':''}"></span></button>
      <button class="item" id="pNot">${I.bell}<span class="grow">Уведомления<small>${nOk?'Пока вкладка открыта':'Браузер не поддерживает'}</small></span><span class="sw ${nOn?'on':''}"></span></button></div>
      <div class="card"><div class="pad"><span class="lbl">Как защищена переписка</span><span class="note">Каждое сообщение шифруется на вашем устройстве ключом, который знают только вы и собеседник (X25519 + AES-256-GCM). На сервере хранятся шифротекст, ваше имя, ID и время сообщений. Ваш секретный ключ хранится на сервере только в зашифрованном виде: расшифровать его можно лишь ключом аккаунта.</span></div></div>
      <div class="card"><button class="item danger" id="pOut">${I.exit}<span class="grow">Выйти</span></button></div>`;
    const n=body.querySelector('#pName');n.value=me.name;
    body.querySelector('#pId').onclick=()=>copy(me.id,'ID скопирован');
    const pa=body.querySelector('#pAdm');if(pa)pa.onclick=adminPage;body.querySelector('#pRules').onclick=()=>rulesSheet(false);
    const pn=body.querySelector('#pNick'),pst=body.querySelector('#pNickSt'),pns=body.querySelector('#pNickSave');pn.value=me.nick||'';
    const stTxt=(t,cls)=>{pst.textContent=t;pst.style.color=cls==='ok'?'#2fa36b':cls==='bad'?'var(--danger)':''};let nt,nseq=0;
    pn.oninput=()=>{pn.value=pn.value.replace(/^@/,'').replace(/\s/g,'');clearTimeout(nt);const v=pn.value;const s=++nseq;
      if(!v)return stTxt(me.nick?'Сохраните пустое поле, чтобы убрать никнейм':'Введите никнейм');
      if(v.toLowerCase()===(me.nick||'').toLowerCase()&&v===me.nick)return stTxt('Это ваш никнейм','ok');
      if(!NICK_RE.test(v))return stTxt('Латинские буквы, цифры и _, от 4 до 32 символов, первая — буква','bad');
      stTxt('Проверяю…');nt=setTimeout(async()=>{try{const r=await api('nickfree?nick='+encodeURIComponent(v));if(s===nseq)stTxt(r.free?'@'+v+' свободен':r.error,r.free?'ok':'bad')}catch(e){}},350)};
    pns.onclick=async()=>{const v=pn.value.trim();if(v&&!NICK_RE.test(v))return toast('Неверный никнейм');try{const r=await api('nick',{nick:v});me.nick=r.nick;saveLocal();toast(r.nick?'Никнейм @'+r.nick+' сохранён':'Никнейм убран');Pages.prof()}catch(e){stTxt(e.message,'bad');toast(e.message)}};
    const pl=body.querySelector('#pLink');if(pl)pl.onclick=()=>copy(location.origin+'/#@'+me.nick,'Ссылка скопирована');
    body.querySelector('#pSave').onclick=async()=>{const v=n.value.trim();if(!v)return toast('Введите имя');try{const r=await api('me',{name:v});me.name=r.name;saveLocal();$('#meName').textContent=me.name;toast('Сохранено');Pages.prof()}catch(e){toast(e.message)}};
    body.querySelector('#pKey').onclick=async()=>{if(await confirmSheet('Показать ключ?','Убедитесь, что никто не видит экран.','Показать'))sheet(`<div class="sh">Ключ аккаунта</div><div class="pad"><div class="keybox">${esc(me.ak)}</div><span class="note">С этим ключом можно войти в аккаунт с любого устройства.</span></div><div class="acts"><button class="btn ghost" id="kC">${I.copy} Копировать</button><button class="btn" id="kD">Готово</button></div>`,el=>{el.querySelector('#kC').onclick=()=>copy(me.ak,'Ключ скопирован');el.querySelector('#kD').onclick=()=>back()})};
    body.querySelector('#pTheme').onclick=()=>{localStorage.setItem(LS+'.theme',isDark()?'light':'dark');applyTheme();Pages.prof()};
    body.querySelector('#pNot').onclick=async()=>{if(!nOk)return;if(!nOn){let p=Notification.permission;if(p!=='granted')try{p=await Notification.requestPermission()}catch(e){}if(p!=='granted')return toast('Разрешите уведомления в браузере');localStorage.setItem(LS+'.notify','1')}else localStorage.removeItem(LS+'.notify');Pages.prof()};
    body.querySelector('#pOut').onclick=()=>logout(false);
  },'prof');
}
function handleHash(){const raw=decodeURIComponent((location.hash||'').slice(1));const nk=raw.replace(/^@/,'');
  if(raw.startsWith('@')&&NICK_RE.test(nk)){history.replaceState(null,'','/');api('user?id='+encodeURIComponent(nk)).then(u=>{if(u.id!==me.id){chat(u);openChat(u.id)}}).catch(e=>toast(e.message));return}
  const h=raw.replace(/\D/g,'');if(h.length===9){history.replaceState(null,'','/');if(h!==me.id)openChat(h)}}


/* ---------- лента: публичные посты ---------- */
let tab=(()=>{try{return localStorage.getItem(LS+'.tab')||'chats'}catch(e){return'chats'}})();
const feed={items:[],more:false,f:{tag:'',q:'',author:'',name:''},loading:false,loaded:false,fresh:false,tags:[],seq:0};
const posts=new Map();
let postPage=null,npDraft={text:'',imgs:[]};
function setTab(t){
  tab=['feed','game'].includes(t)?t:'chats';try{localStorage.setItem(LS+'.tab',tab)}catch(e){}
  $('#tabs').querySelectorAll('[data-tab]').forEach(b=>b.classList.toggle('on',b.dataset.tab===tab));
  $('#chatsView').hidden=tab!=='chats';$('#feedView').hidden=tab!=='feed';$('#gameView').hidden=tab!=='game';$('#fab').hidden=tab==='game';
  if(tab==='game')renderGame();
  $('#fab').innerHTML=tab==='feed'?I.plus:I.pen;$('#fab').setAttribute('aria-label',tab==='feed'?'Новый пост':'Найти человека');
  $('#feedNew').hidden=!(tab==='feed'&&feed.fresh);
  if(tab==='feed'&&!feed.loaded&&me)loadFeed();else renderFeed();
  if(tab==='feed'&&me&&!rulesOk())rulesSheet(true);
}
function richText(s){
  const re=/(https?:\/\/[^\s<]+[^\s<.,;:!?)\]'"])|#([\p{L}\p{N}_]{1,50})/gu;let out='',i=0;
  for(const m of s.matchAll(re)){out+=esc(s.slice(i,m.index));
    out+=m[1]?`<a href="${esc(m[1])}" target="_blank" rel="noopener noreferrer">${esc(m[1])}</a>`:`<a data-tag="${esc(m[2].toLowerCase())}">#${esc(m[2])}</a>`;i=m.index+m[0].length}
  return out+esc(s.slice(i));
}
function fmtAgo(ms){const s=(Date.now()-ms)/1000;if(s<60)return'сейчас';if(s<3600)return Math.floor(s/60)+' мин';if(s<86400)return Math.floor(s/3600)+' ч';const d=new Date(ms),n=new Date();return d.toLocaleDateString('ru-RU',{day:'numeric',month:'short',year:d.getFullYear()===n.getFullYear()?undefined:'numeric'}).replace('.','')}
const avU=(u,cls='sm')=>`<div class="av ${cls} c${hashN(u.id,7)}" data-author="${esc(u.id)}">${esc(([...(u.name||'?').trim()][0]||'?').toUpperCase())}</div>`;
function upsertPost(p){const o=posts.get(p.id);if(o){Object.assign(o,p);return o}posts.set(p.id,p);return p}
function postHTML(p,full){
  const imgs=(p.imgs||[]).filter(u=>/^\/img\/[0-9a-f]{24}\.jpg$/.test(u));
  return`<article class="post ${full?'full':''}" data-post="${esc(p.id)}">${avU(p.author)}<div class="pb">
    <div class="ph"><b data-author="${esc(p.author.id)}">${esc(p.author.name)}</b><span class="tm">${fmtAgo(p.ts)}</span><button class="ib pm" data-pmenu aria-label="Ещё">${I.dots}</button></div>
    ${p.pending?'<div class="plbl">На проверке у модератора: пока пост видите только вы</div>':''}${p.hidden&&!p.pending?'<div class="plbl">Скрыт по жалобам: ждёт решения модератора</div>':''}
    ${p.text?`<div class="ptxt ${full?'':'clip'}">${richText(p.text)}</div>`:''}
    ${imgs.length?`<div class="pimgs n${imgs.length}">${imgs.map(u=>`<img src="${u}" alt="Картинка" loading="lazy" data-view>`).join('')}</div>`:''}
    <div class="pact"><button data-like class="${p.liked?'liked':''}" aria-label="Нравится">${I.heart}<span>${p.likes||''}</span></button><button data-open aria-label="Комментарии">${I.cmt}<span>${p.cmts||''}</span></button></div>
  </div></article>`;
}
function updatePost(p,pop){document.querySelectorAll(`[data-post="${CSS.escape(p.id)}"]`).forEach(el=>{el.outerHTML=postHTML(p,el.classList.contains('full'))});
  if(pop)document.querySelectorAll(`[data-post="${CSS.escape(p.id)}"] [data-like]`).forEach(b=>b.classList.add('pop'))}
function dropPost(id){posts.delete(id);feed.items=feed.items.filter(p=>p.id!==id);renderFeed();if(postPage&&postPage.id===id){toast('Пост удалён');back()}}
async function loadFeed(more){
  if(more&&feed.loading)return;const seq=++feed.seq;feed.loading=true;if(!more){feed.fresh=false;$('#feedNew').hidden=true;renderFeed()}
  try{const q=new URLSearchParams();const f=feed.f;if(more&&feed.items.length)q.set('before',feed.items.at(-1).ts);
    if(f.tag)q.set('tag',f.tag);if(f.q)q.set('q',f.q);if(f.author)q.set('author',f.author);
    const [r,tags]=await Promise.all([api('feed?'+q),!more&&!f.tag&&!f.q&&!f.author?api('tags').catch(()=>[]):null]);
    if(seq!==feed.seq)return;const list=r.posts.map(upsertPost);
    feed.items=more?[...feed.items,...list.filter(p=>!feed.items.includes(p))]:list;feed.more=r.more;feed.loaded=true;if(tags)feed.tags=tags;
  }catch(e){if(seq===feed.seq)toast(e.message)}
  if(seq===feed.seq){feed.loading=false;renderFeed()}
}
function renderFeed(){
  if(tab!=='feed'||$('#home').hidden)return;const f=feed.f,box=$('#feed');let h='';
  if(f.author)h+=`<div class="fhead"><button class="chip" data-clear>Посты: ${esc(f.name)} ${I.close}</button></div>`;
  else if(f.tag)h+=`<div class="fhead"><button class="chip" data-clear>#${esc(f.tag)} ${I.close}</button></div>`;
  else if(f.q)h+=`<div class="fhead"><button class="chip" data-clear>«${esc(f.q)}» ${I.close}</button></div>`;
  else if(feed.tags.length)h+=`<div class="tagbar">${feed.tags.map(t=>`<button class="chip" data-tag="${esc(t.tag)}">#${esc(t.tag)} <small>${t.n}</small></button>`).join('')}</div>`;
  const items=feed.items.filter(p=>!blocked.has(p.author.id));
  h+=items.map(p=>postHTML(p)).join('');
  if(feed.loading)h+='<div class="empty"><div class="spin"></div></div>';
  else if(!items.length)h+=f.tag||f.q||f.author?`<div class="empty"><div class="glyph">${I.search}</div><h3>Ничего не найдено</h3><p>${f.tag?'Постов с этим хештегом пока нет.':f.author?'Этот человек ещё ничего не публиковал.':'Попробуйте другое слово.'}</p></div>`
    :`<div class="empty"><div class="glyph">${I.hash}</div><h3>Лента пока пустая</h3><p>Напишите первый пост. Его увидят все пользователи сервера.</p><button class="btn" data-newpost>${I.plus} Написать пост</button></div>`;
  else if(feed.more)h+='<button class="more" data-feedmore style="display:block;margin:12px auto">Показать ещё</button>';
  else if(items.length>3)h+='<div class="fend">Это все посты</div>';
  box.innerHTML=h;
}
function setFilter(f,typed){
  feed.f=Object.assign({tag:'',q:'',author:'',name:''},f);if(!typed)$('#fq').value=feed.f.tag?'#'+feed.f.tag:feed.f.q;
  const go=()=>{if(tab!=='feed')setTab('feed');$('#feed').scrollTop=0;loadFeed()};
  if(layers.length)back(layers.length).then(go);else go();
}
function viewImg(src){const v=$('#viewer');v.innerHTML=`<img src="${esc(src)}" alt="Фото">`;v.hidden=false;v.onclick=()=>{v.hidden=true;v.innerHTML=''}}
async function toggleLike(id){
  const p=posts.get(id);if(!p)return;const on=!p.liked;p.liked=on;p.likes=Math.max(0,(p.likes||0)+(on?1:-1));updatePost(p,on);
  try{const r=await api('like',{post:id,on});p.likes=r.likes;p.liked=r.liked;updatePost(p)}catch(e){p.liked=!on;p.likes=Math.max(0,p.likes+(on?-1:1));updatePost(p);toast(e.message)}
}
function authorMenu(id){
  const p=[...posts.values()].find(x=>x.author.id===id);const name=p?p.author.name:(postPage&&postPage.cmts.find(c=>c.author.id===id)||{author:{name:'ID '+fmtId(id)}}).author.name;
  menuSheet([
    {icon:I.hash,label:id===me.id?'Мои посты':'Посты пользователя',go:()=>setFilter({author:id,name})},
    id!==me.id&&{icon:I.bubble,label:'Написать сообщение',go:()=>openChat(id)},
    {icon:I.copy,label:'Скопировать ID',sub:fmtId(id),go:()=>copy(id,'ID скопирован')},
  ],`<div class="sh">${esc(name)}${(p&&p.author.nick)?` <small style="color:var(--muted);font-weight:500">@${esc(p.author.nick)}</small>`:''}</div>`);
}
function postMenu(id){
  const p=posts.get(id);if(!p)return;const mine=p.author.id===me.id;
  menuSheet([
    p.text&&{icon:I.copy,label:'Копировать текст',go:()=>copy(p.text)},
    !mine&&{icon:I.bubble,label:'Написать автору',go:()=>openChat(p.author.id)},
    {icon:I.user,label:mine?'Мои посты':'Все посты автора',go:()=>setFilter({author:p.author.id,name:p.author.name})},
    !mine&&{icon:I.alert,label:'Пожаловаться',go:()=>reportSheet('post',id)},
    me.admin&&p.pending&&{icon:I.check,label:'Одобрить публикацию',go:()=>api('admin/approve',{id}).then(()=>{p.pending=false;updatePost(p);modCount();toast('Опубликовано')}).catch(e=>toast(e.message))},
    (mine||me.admin)&&{icon:I.trash,label:'Удалить пост',danger:true,go:async()=>{if(!await confirmSheet('Удалить пост?','Пост, картинки и комментарии к нему будут удалены у всех.','Удалить',true))return;try{await api('delpost',{id});dropPost(id)}catch(e){toast(e.message)}}},
    me.admin&&!mine&&{icon:I.block,label:'Заблокировать автора',danger:true,go:()=>banMenu({id:p.author.id,name:p.author.name})},
  ],p.text?`<div class="preview">${esc(p.text.slice(0,200))}</div>`:'');
}
function postClick(e,inFeed){
  const t=e.target;
  const tg=t.closest('[data-tag]');if(tg){setFilter({tag:tg.dataset.tag});return}
  if(t.closest('a'))return;
  const post=t.closest('[data-post]'),id=post&&post.dataset.post;
  const au=t.closest('[data-author]');if(au){authorMenu(au.dataset.author);return}
  if(t.closest('[data-view]')){viewImg(t.closest('[data-view]').src);return}
  if(!id)return;
  if(t.closest('[data-like]')){toggleLike(id);return}
  if(t.closest('[data-pmenu]')){postMenu(id);return}
  if(inFeed&&!getSelection().toString())openPost(id);
}
function cmtHTML(c){return`<div class="cmt" data-cmt="${esc(c.id)}">${avU(c.author,'xs')}<div class="cb"><div class="ph"><b data-author="${esc(c.author.id)}">${esc(c.author.name)}</b><span class="tm">${fmtAgo(c.ts)}</span></div><div class="ctxt">${richText(c.text)}</div></div></div>`}
function renderCmts(scroll){
  if(!postPage)return;const el=postPage.body.querySelector('#cmts');if(!el)return;
  el.innerHTML=postPage.cmts.length?postPage.cmts.map(cmtHTML).join(''):'<div class="cmts-empty">Комментариев пока нет. Будьте первым.</div>';
  if(scroll)postPage.body.scrollTop=postPage.body.scrollHeight;
}
function openPost(id){
  const p0=posts.get(id);
  openPage('Пост',(body,foot)=>{
    postPage={id,cmts:[],body};
    body.innerHTML=(p0?postHTML(p0,true):'<div class="empty"><div class="spin"></div></div>')+'<div class="sec">Комментарии</div><div id="cmts"><div class="empty"><div class="spin"></div></div></div>';
    foot.innerHTML=`<textarea class="cinp" rows="1" maxlength="1000" placeholder="Комментарий" enterkeyhint="send"></textarea><button class="ib go" aria-label="Отправить">${I.send}</button>`;
    const ta=foot.querySelector('textarea'),sb=foot.querySelector('button');
    const size=()=>{ta.style.height='auto';ta.style.height=Math.min(ta.scrollHeight,160)+'px'};ta.oninput=size;
    const send=async()=>{const text=ta.value.trim();if(!text)return;sb.disabled=true;
      try{const c=await api('comment',{post:id,text});ta.value='';size();if(!postPage.cmts.some(x=>x.id===c.id))postPage.cmts.push(c);const p=posts.get(id);if(p){p.cmts=postPage.cmts.length;updatePost(p)}renderCmts(true)}catch(e){toast(e.message)}sb.disabled=false};
    sb.onclick=send;ta.onkeydown=e=>{if(e.key==='Enter'&&!e.shiftKey&&!coarse){e.preventDefault();send()}};
    body.onclick=e=>{
      const cm=e.target.closest('[data-cmt]');
      if(cm&&!e.target.closest('[data-author],[data-tag],a')){const c=postPage.cmts.find(x=>x.id===cm.dataset.cmt);const p=posts.get(id);if(!c)return;
        menuSheet([{icon:I.copy,label:'Копировать текст',go:()=>copy(c.text)},
          c.author.id!==me.id&&{icon:I.reply,label:'Ответить',go:()=>{ta.value=c.author.name+', '+ta.value;size();ta.focus()}},
          c.author.id!==me.id&&{icon:I.alert,label:'Пожаловаться',go:()=>reportSheet('cmt',c.id)},
          me.admin&&c.author.id!==me.id&&{icon:I.block,label:'Заблокировать автора',danger:true,go:()=>banMenu({id:c.author.id,name:c.author.name})},
          (c.author.id===me.id||(p&&p.author.id===me.id)||me.admin)&&{icon:I.trash,label:'Удалить комментарий',danger:true,go:async()=>{try{await api('delcmt',{id:c.id});postPage.cmts=postPage.cmts.filter(x=>x!==c);if(p){p.cmts=postPage.cmts.length;updatePost(p)}renderCmts()}catch(err){toast(err.message)}}}],
          `<div class="preview">${esc(c.text.slice(0,200))}</div>`);return}
      postClick(e,false)};
    api('comments?post='+id).then(r=>{if(!postPage||postPage.id!==id)return;const p=upsertPost(r.post);postPage.cmts=r.cmts;
      const art=body.querySelector('[data-post]');if(art)art.outerHTML=postHTML(p,true);else body.querySelector('.empty').outerHTML=postHTML(p,true);renderCmts()})
      .catch(e=>{toast(e.message);if(e.message.includes('удалён'))dropPost(id)});
  },null,{footer:true,onClose(){postPage=null}});
}
function newPostPage(){
  openPage('Новый пост',(body,foot)=>{
    body.innerHTML=`<div class="np">${avU({id:me.id,name:me.name})}<textarea maxlength="3000" placeholder="Что нового? Добавьте #хештеги, чтобы пост было легче найти"></textarea></div><div class="npimgs"></div><div class="pubnote">Пост увидят все пользователи этого сервера. Публичные посты не шифруются сквозным шифрованием, в отличие от личных сообщений. Запрещены порнография, нацизм, экстремизм и призывы к насилию: за это аккаунт блокируется.</div>`;
    foot.innerHTML=`<button class="ib" data-att aria-label="Добавить картинки">${I.photo}</button><span class="cnt" style="flex:1"></span><button class="btn" data-pub>Опубликовать</button>`;
    const ta=body.querySelector('textarea'),grid=body.querySelector('.npimgs'),cnt=foot.querySelector('.cnt'),pub=foot.querySelector('[data-pub]');
    ta.value=npDraft.text;
    const upd=()=>{npDraft.text=ta.value;const n=ta.value.length;cnt.textContent=n>2500?(3000-n)+'':'';pub.disabled=!ta.value.trim()&&!npDraft.imgs.length;
      grid.innerHTML=npDraft.imgs.map((d,i)=>`<div><img src="${d}" alt=""><button data-rm="${i}" aria-label="Убрать">${I.close}</button></div>`).join('')};
    ta.oninput=upd;upd();setTimeout(()=>ta.focus(),320);
    grid.onclick=e=>{const b=e.target.closest('[data-rm]');if(b){npDraft.imgs.splice(+b.dataset.rm,1);upd()}};
    foot.querySelector('[data-att]').onclick=()=>{if(npDraft.imgs.length>=4)return toast('Не больше 4 картинок');$('#fPost').click()};
    $('#fPost').onchange=async e=>{const files=[...e.target.files].slice(0,4-npDraft.imgs.length);e.target.value='';
      for(const f of files){try{npDraft.imgs.push(await compressImage(f,1600,560000))}catch(err){toast(err.message)}}upd()};
    pub.onclick=async()=>{const text=ta.value.trim();if(!text&&!npDraft.imgs.length)return;
      const imgs=npDraft.imgs.map(d=>d.replace(/^data:image\/jpeg;base64,/,''));if(imgs.some(x=>x.startsWith('data:')))return toast('Формат картинки не поддерживается');
      pub.disabled=true;pub.textContent='Публикую…';
      try{const p=upsertPost(await api('post',{text,imgs}));npDraft={text:'',imgs:[]};
        feed.f={tag:'',q:'',author:'',name:''};$('#fq').value='';feed.items=[p,...feed.items.filter(x=>x!==p)];
        await back();if(tab!=='feed')setTab('feed');$('#feed').scrollTop=0;renderFeed();loadFeed();toast(p.pending?'Пост с картинками отправлен на проверку. Его увидят после одобрения модератором':'Опубликовано')}
      catch(e){toast(e.message);pub.disabled=false;pub.textContent='Опубликовать'}};
  },null,{footer:true});
}
function wireFeed(){
  const box=$('#feed');
  box.addEventListener('click',e=>{
    if(e.target.closest('[data-clear]')){setFilter({});return}
    if(e.target.closest('[data-feedmore]')){loadFeed(true);return}
    if(e.target.closest('[data-newpost]')){newPostPage();return}
    postClick(e,true);
  });
  box.addEventListener('scroll',()=>{if(feed.more&&!feed.loading&&box.scrollHeight-box.scrollTop-box.clientHeight<600)loadFeed(true)},{passive:true});
  let t;$('#fq').oninput=()=>{clearTimeout(t);t=setTimeout(()=>{const v=$('#fq').value.trim();
    if(v.startsWith('#')){const tag=(v.slice(1).match(/^[\p{L}\p{N}_]+/u)||[''])[0].toLowerCase();if(tag)setFilter({tag},true)}
    else setFilter(v?{q:v}:{},true)},450)};
  $('#fq').onkeydown=e=>{if(e.key==='Enter'){e.preventDefault();$('#fq').blur()}};
  $('#feedNew').onclick=()=>{box.scrollTop=0;loadFeed()};
}

/* ---------- вкладка «Игра» ---------- */
let gameT=null;
async function renderGame(){
  clearTimeout(gameT);if(tab!=='game'||$('#home').hidden)return;const box=$('#gameBox');
  if(!box.innerHTML)box.innerHTML=`<div class="arena"><div class="arena-hero tanks-hero"><div class="arena-t tanks-t">ТАНКИ</div><div class="arena-s">Онлайн-битвы на танках · 4 режима · 8 карт</div></div>
    <div id="tRooms"></div>
    <div class="card"><div class="pad"><span class="lbl">Техника</span><div class="arena-w"><span><b>7 корпусов</b> Оса · Шершень · Хантер · Викинг · Диктатор · Титан · Мамонт</span><span><b>11 пушек</b> Смоки · Огнемёт · Твинс · Рельса · Изида · Молот · Тандер · Фриз · Рикошет · Шафт · Вулкан</span><span><b>5 припасов</b> ремкомплект · броня · урон · ускорение · мины</span><span><b>Ящики</b> с неба и золотой ящик на 1000 💎</span><span><b>30 званий</b> и кристаллы</span></div>
      <span class="note">Характеристики M0–M3, цены и звания — как в «Танках Онлайн». ${coarse?'Джойстик — езда, кнопки ◀ ▶ — башня, ОГОНЬ — выстрел, припасы — кнопки вверху.':'WASD — езда, стрелки ← → — башня, пробел — огонь, 1–5 — припасы.'}</span></div></div>
  </div><div class="arena"><div class="arena-hero"><div class="arena-t">АРЕНА</div><div class="arena-s">3D-шутер от первого лица · онлайн-дезматч</div></div>
    <div id="gMaps"></div>
    <div class="card"><div class="pad"><span class="lbl">Оружие</span><div class="arena-w">${['Бластер','Дробовик','Автомат','Гранатомёт','Ракетница','Гипербластер','Рельсотрон','Огнемёт','BFG'].map((n,i)=>`<span><b>${i+1}</b> ${n}</span>`).join('')}</div>
      <span class="note">Ракету под ноги + прыжок — рокет-джамп. На телефоне есть лёгкая помощь в прицеливании, её можно выключить в меню игры.</span></div></div>
    <div class="card"><div class="pad"><span class="lbl">Управление</span><span class="note">${coarse?'Левая половина экрана — ходьба, правая — обзор. Кнопки: огонь, прыжок, смена оружия. Играйте горизонтально.':'WASD — ходьба, мышь — обзор и огонь, пробел — прыжок, 1–9 или колесо — оружие, Tab — счёт, Esc — меню.'}</span></div></div></div>`;
  const D={arena:['Тёмная арена: лава, базы, коридор, башня','arena-dark'],town:['Дневной городок: канал с мостами, дома, крыши, рынок, собор','arena-day']};
  try{const r=await api('tanks/info'),by={},MD={dm:'Все против всех',tdm:'Команды',ctf:'Захват флага',cp:'Контроль точек'},D={'Песочница':'маленькая: высота в центре, пандусы, снайперские площадки','Тишина':'синие на высоком берегу, красные внизу: пандус, склон и центр',
      'Мосты':'четыре базы на высоте, шесть мостов, центр с большим зданием','Кунгур':'два холма с мостом через долину, два городка, полка и стена','Серпухов':'зеркальные базы, открытая середина, длинный склон, мост и проезд под ним','Полигон':'большая: канава с мостами и бродами, холмы, здания','Колхоз':'длинное поле: базы на концах, гора с седловиной, северный и южный проходы','Рио':'пляж, фавелы на возвышении, многоэтажная парковка в центре'};
    for(const x of r.rooms)(by[x.map]=by[x.map]||[]).push(x);
    $('#tRooms').innerHTML=`<div class="tk-prof">⭐ ${esc(r.rankName)} · ${r.xp.toLocaleString('ru-RU')} опыта · 💎 ${r.cr.toLocaleString('ru-RU')}</div>`+Object.entries(by).map(([mp,l],i)=>`<div class="mapcard ${['tk-sand','tk-gray','tk-sky','tk-eve','tk-sky','tk-poly','tk-sand','tk-poly'][i]||'tk-sand'}"><div class="mc-t">${esc(mp)}</div><div class="mc-d">${D[mp]||''}</div>
      <div class="tk-btns">${l.map(x=>`<button class="btn tanks-go" data-room="${x.id}">▶ ${MD[x.mode]}${x.players.length?` (${x.players.length})`:''}</button>`).join('')}</div>
      ${l.some(x=>x.players.length)?`<div class="mc-p">В бою: ${l.flatMap(x=>x.players).map(esc).join(', ')}</div>`:''}</div>`).join('');
    $('#tRooms').onclick=e=>{const b=e.target.closest('[data-room]');if(b)location.href='/tanks?room='+b.dataset.room}}catch(e){}
  try{const r=await api('game/info');$('#gMaps').innerHTML=r.maps.map(m=>{const n=m.players.length,d=D[m.id]||['',''];return`<div class="mapcard ${d[1]}"><div class="mc-t">${esc(m.name)}</div><div class="mc-d">${d[0]}</div>
      <div class="mc-p">${n?`Играют (${n}): ${m.players.map(esc).join(', ')}`:'Сейчас пусто'}</div><button class="btn arena-go" data-map="${m.id}">▶ Играть</button></div>`}).join('');
    $('#gMaps').onclick=e=>{const b=e.target.closest('[data-map]');if(b)location.href='/game?map='+b.dataset.map}}catch(e){}
  gameT=setTimeout(renderGame,5000);
}

/* ---------- правила, жалобы, модерация ---------- */
const RULES_V='1';
function rulesOk(){try{return localStorage.getItem(LS+'.rules')===RULES_V}catch(e){return true}}
function rulesSheet(need){
  sheet(`<div class="sh">Правила ленты</div><div class="rules"><p>Мат можно. Запрещено:</p><ul>
    <li>порнография и откровенные фото в любом виде;</li><li>нацизм, фашистская символика, экстремизм;</li>
    <li>призывы к насилию, травля, угрозы;</li><li>спам и мошенничество.</li></ul>
    <p>Посты с картинками публикуются после проверки модератором. Если видите нарушение, нажмите ⋮ → «Пожаловаться». Пост, на который пожаловались три человека, скрывается до решения модератора. За нарушения аккаунт блокируется.</p></div>
    <div class="acts"><button class="btn" data-ok>${need?'Принимаю':'Понятно'}</button></div>`,el=>{el.querySelector('[data-ok]').onclick=()=>{try{localStorage.setItem(LS+'.rules',RULES_V)}catch(e){}back()}});
}
function reportSheet(kind,id){
  const R=[['porn','Порнография'],['nazi','Нацизм, экстремизм'],['violent','Насилие, жестокость'],['spam','Спам, мошенничество'],['other','Другое']];
  menuSheet(R.map(([k,l])=>({label:l,go:()=>api('report',{kind,id,reason:k}).then(()=>toast('Жалоба отправлена модератору. Спасибо!')).catch(e=>toast(e.message))})),`<div class="sh">Пожаловаться${kind==='post'?' на пост':kind==='cmt'?' на комментарий':' на пользователя'}</div>`);
}
let modN=0;
async function modCount(){if(!me.admin)return;try{const r=await api('admin/queue');modN=r.pending.length+r.reports.length}catch(e){return}
  const b=$('#bMenu');let d=b.querySelector('.mdot');if(modN&&!d){d=document.createElement('span');d.className='mdot';b.append(d)}else if(!modN&&d)d.remove();if(Pages.admin&&adminTab==='queue')Pages.admin()}
function banMenu(u,after){
  menuSheet([
    {icon:I.block,label:'Заблокировать',sub:'Не сможет войти. Посты скроются',go:()=>doBan(u,true,false,after)},
    {icon:I.trash,label:'Заблокировать и удалить все посты',sub:'Посты, картинки и комментарии удалятся',danger:true,go:()=>doBan(u,true,true,after)},
  ],`<div class="sh">${esc(u.name||'Пользователь')}</div>`);
}
async function doBan(u,ban,purge,after){
  try{await api('admin/ban',{id:u.id,ban,purge});toast(ban?'Пользователь заблокирован':'Пользователь разблокирован');
    if(ban){for(const p of [...posts.values()])if(p.author.id===u.id&&purge)dropPost(p.id);feed.items=feed.items.filter(p=>p.author.id!==u.id);renderFeed()}
    modCount();if(after)after()}catch(e){toast(e.message)}
}
let adminTab='queue',admQ='';
function adminPage(){
  openPage('Админ-панель',async body=>{
    const tabs=`<nav class="tabs admtabs" id="admTabs">${[['queue','Проверка'+(modN?` <span class="cnt">${modN}</span>`:'')],['users','Пользователи'],['set','Настройки']].map(([k,l])=>`<button data-at="${k}" class="${adminTab===k?'on':''}" style="flex:1;justify-content:center">${l}</button>`).join('')}</nav>`;
    body.innerHTML=tabs+'<div id="admBody"><div class="empty"><div class="spin"></div></div></div>';
    body.querySelector('#admTabs').onclick=e=>{const b=e.target.closest('[data-at]');if(b){adminTab=b.dataset.at;Pages.admin()}};
    const box=body.querySelector('#admBody');
    try{if(adminTab==='queue')await admQueue(box);else if(adminTab==='users')await admUsers(box);else await admSettings(box)}catch(e){box.innerHTML=`<div class="empty"><p>${esc(e.message)}</p></div>`}
  },'admin');
}
async function admQueue(box){
  const r=await api('admin/queue');modN=r.pending.length+r.reports.length;
  let h='';
  if(r.pending.length)h+=`<div class="sec">Ждут проверки · ${r.pending.length}</div>`+r.pending.map(p=>{upsertPost(p);return`<div class="qitem">${postHTML(p,true)}<div class="qacts"><button class="btn sm" data-a="approve" data-t="${esc(p.id)}">Опубликовать</button><button class="btn sm ghost" data-a="delpost" data-t="${esc(p.id)}">Отклонить</button><button class="btn sm danger" data-a="ban" data-u="${esc(p.author.id)}" data-n="${esc(p.author.name)}">Блок автора</button></div></div>`}).join('');
  if(r.reports.length)h+=`<div class="sec">Жалобы · ${r.reports.length}</div>`+r.reports.map(it=>{
    const what=it.kind==='post'?'Пост':it.kind==='cmt'?'Комментарий':'Пользователь';
    let body='',au=null;
    if(it.post){upsertPost(it.post);body=postHTML(it.post,true);au=it.post.author}
    else if(it.cmt){body=cmtHTML(it.cmt);au=it.cmt.author}
    else if(it.user){const u=it.user;au=u;body=`<div class="row" style="cursor:default">${avU(u)}<div class="rb"><div class="r1"><b>${esc(u.name)}${u.banned?'<span class="ulbl ban">заблокирован</span>':''}</b></div><div class="r2"><span class="pv">${u.nick?'@'+esc(u.nick)+' · ':''}ID ${fmtId(u.id)} · постов: ${u.posts}</span></div></div></div>`}
    const del=it.kind==='post'?`<button class="btn sm ghost" data-a="delpost" data-t="${esc(it.target)}">Удалить пост</button>`:it.kind==='cmt'?`<button class="btn sm ghost" data-a="delcmt" data-t="${esc(it.target)}">Удалить</button>`:'';
    return`<div class="qitem"><div class="qhead"><b>${what}</b>· жалоб: ${it.count} · ${esc(it.reasons.join(', '))}${it.hidden?' · <span style="color:var(--warn)">скрыт</span>':''}</div>${body}
      <div class="qacts">${del}<button class="btn sm ghost" data-a="dismiss" data-k="${it.kind}" data-t="${esc(it.target)}">Нарушения нет</button>${au&&!au.admin?`<button class="btn sm danger" data-a="ban" data-u="${esc(au.id)}" data-n="${esc(au.name)}">Блок ${it.kind==='user'?'':'автора'}</button>`:''}</div></div>`}).join('');
  box.innerHTML=h||`<div class="empty"><div class="glyph">${I.shield}</div><h3>Всё чисто</h3><p>Нет постов на проверке и новых жалоб.</p></div>`;
  const b=$('#bMenu .mdot');if(!modN&&b)b.remove();
  box.onclick=async e=>{
    const a=e.target.closest('[data-a]');
    if(!a){const t=e.target;const tg=t.closest('[data-tag]');if(tg)return setFilter({tag:tg.dataset.tag});const im=t.closest('[data-view]');if(im)viewImg(im.src);return}
    const act=a.dataset.a,t=a.dataset.t;
    try{
      if(act==='approve'){await api('admin/approve',{id:t});const p=posts.get(t);if(p)p.pending=false;toast('Опубликовано')}
      else if(act==='delpost'){if(!await confirmSheet('Удалить пост?','Пост, картинки и комментарии удалятся у всех.','Удалить',true))return;await api('delpost',{id:t});dropPost(t);toast('Пост удалён')}
      else if(act==='delcmt'){await api('delcmt',{id:t});toast('Комментарий удалён')}
      else if(act==='dismiss'){await api('admin/dismiss',{kind:a.dataset.k,id:t});toast('Жалобы сняты')}
      else if(act==='ban'){banMenu({id:a.dataset.u,name:a.dataset.n},()=>Pages.admin&&Pages.admin());return}
      modCount();Pages.admin();
    }catch(err){toast(err.message)}
  };
}
async function admUsers(box){
  box.innerHTML=`<div id="srch"><input class="field" id="admQ" type="search" placeholder="Имя, @ник или ID" autocomplete="off"></div><div id="admList"><div class="empty"><div class="spin"></div></div></div>`;
  const inp=box.querySelector('#admQ');inp.value=admQ;let t;
  const load=async()=>{const r=await api('admin/users?q='+encodeURIComponent(admQ));const L=box.querySelector('#admList');if(!L)return;
    L.innerHTML=`<div class="qhead">Всего пользователей: ${r.total}${admQ?' · найдено: '+r.users.length:''}</div>`+r.users.map(u=>`<div class="row" data-uid="${esc(u.id)}">${avU(u)}<div class="rb"><div class="r1"><b>${esc(u.name)}${u.admin?'<span class="ulbl adm">админ</span>':''}${u.banned?'<span class="ulbl ban">заблокирован</span>':''}</b><span class="tm">${u.online?'в сети':''}</span></div><div class="r2"><span class="pv">${u.nick?'@'+esc(u.nick)+' · ':''}ID ${fmtId(u.id)} · постов: ${u.posts} · с ${new Date(u.created*1000).toLocaleDateString('ru-RU')}</span></div></div></div>`).join('');
    L.onclick=e=>{const row=e.target.closest('[data-uid]');if(!row)return;const u=r.users.find(x=>x.id===row.dataset.uid);if(u)userAdminMenu(u)}};
  inp.oninput=()=>{clearTimeout(t);t=setTimeout(()=>{admQ=inp.value.trim();load().catch(e=>toast(e.message))},350)};
  await load();
}
function userAdminMenu(u){
  const re=()=>Pages.admin&&Pages.admin();
  menuSheet([
    {icon:I.hash,label:'Посты пользователя',go:()=>setFilter({author:u.id,name:u.name})},
    u.id!==me.id&&{icon:I.bubble,label:'Написать сообщение',go:()=>openChat(u.id)},
    {icon:I.copy,label:'Скопировать ID',sub:fmtId(u.id),go:()=>copy(u.id,'ID скопирован')},
    {icon:I.shield,label:'Танки: звание и кристаллы',sub:'Выдать любое звание, кристаллы, припасы, технику',go:()=>tanksAdmin(u)},
    !u.admin&&u.posts>0&&{icon:I.trash,label:'Удалить все посты и комментарии',danger:true,go:async()=>{if(!await confirmSheet('Удалить все посты?',`Все посты, картинки и комментарии пользователя ${u.name} удалятся у всех. Аккаунт останется.`,'Удалить',true))return;try{const r=await api('admin/purge',{id:u.id});for(const p of [...posts.values()])if(p.author.id===u.id)dropPost(p.id);toast(`Удалено постов: ${r.posts}, комментариев: ${r.cmts}`);re()}catch(e){toast(e.message)}}},
    !u.admin&&(u.banned?{icon:I.check,label:'Разблокировать',go:()=>doBan(u,false,false,re)}:{icon:I.block,label:'Заблокировать',danger:true,go:()=>banMenu(u,re)}),
    !u.admin&&{icon:I.trash,label:'Удалить аккаунт',sub:'Навсегда: аккаунт, посты, переписка с ним',danger:true,go:async()=>{if(!await confirmSheet('Удалить аккаунт навсегда?',`Удалятся аккаунт ${u.name}, его никнейм, все посты, комментарии и вся переписка с ним у всех собеседников. Отменить нельзя.`,'Удалить навсегда',true))return;
      try{await api('admin/deluser',{id:u.id});for(const p of [...posts.values()])if(p.author.id===u.id)dropPost(p.id);chats.delete(u.id);renderList();toast('Аккаунт удалён');re()}catch(e){toast(e.message)}}},
  ],`<div class="sh">${esc(u.name)}${u.nick?` <small style="color:var(--muted);font-weight:500">@${esc(u.nick)}</small>`:''}</div>`);
}
// Танки: выдача званий и кристаллов (только администратор мессенджера)
async function tanksAdmin(u){let r;try{r=await api('tanks/admin?id='+encodeURIComponent(u.id))}catch(e){toast(e.message);return}
  const fmtN=n=>Number(n).toLocaleString('ru-RU');
  const el=sheet(`<div class="sh">Танки · ${esc(u.name)}</div><div class="sbody"><div class="pad" style="display:flex;flex-direction:column;gap:10px">
    <div class="note" id="taNow"></div>
    <span class="lbl">Звание</span><select class="field" id="taRank">${r.ranks.map((n,i)=>`<option value="${i}" ${i===r.rank?'selected':''}>${i+1}. ${esc(n)}</option>`).join('')}</select>
    <button class="btn" id="taRankB">Присвоить звание</button>
    <span class="lbl">Кристаллы</span><input class="field" id="taCr" type="number" inputmode="numeric" min="0" step="1" placeholder="Количество">
    <div class="acts" style="display:flex;gap:8px"><button class="btn ghost" id="taAdd" style="flex:1">Добавить</button><button class="btn" id="taSet" style="flex:1">Установить</button></div>
    <span class="lbl">Ещё</span><div class="acts" style="display:flex;gap:8px;flex-wrap:wrap"><button class="btn ghost" id="taSup" style="flex:1">Припасы по 1000</button><button class="btn ghost" id="taAll" style="flex:1">Вся техника M3</button></div>
    <span class="note">Изменения сразу видны игроку, даже если он сейчас в бою. Выдавать может только администратор.</span></div></div>`);
  const show=x=>{const n=el.querySelector('#taNow');if(!n)return;n.innerHTML=`Сейчас: <b>${esc(x.rankName)}</b> · ${fmtN(x.xp)} опыта · <b>💎 ${fmtN(x.cr)}</b>`;el.querySelector('#taRank').value=x.rank};show(r);
  const go=async(body)=>{try{const x=await api('tanks/admin',Object.assign({id:u.id},body));show(x);toast(x.msg||'Готово')}catch(e){toast(e.message)}};
  const num=()=>{const v=Math.floor(+el.querySelector('#taCr').value);if(!Number.isFinite(v)){toast('Введите число');return null}return v};
  el.querySelector('#taRankB').onclick=()=>go({rank:+el.querySelector('#taRank').value});
  el.querySelector('#taAdd').onclick=()=>{const v=num();if(v!=null)go({add:v})};
  el.querySelector('#taSet').onclick=()=>{const v=num();if(v!=null)go({cr:v})};
  el.querySelector('#taSup').onclick=()=>go({sup:1000});
  el.querySelector('#taAll').onclick=async()=>{if(await confirmSheet('Выдать всю технику?',`${u.name} получит все корпуса и пушки M3 и все краски.`,'Выдать'))go({all:true})};
}
async function admSettings(box){
  const r=await api('admin/settings');
  box.innerHTML=`<div class="card" style="margin-top:0"><button class="item" id="asPre">${I.photo}<span class="grow">Проверка картинок<small>Посты с картинками появляются в ленте только после вашего одобрения. Надёжная защита от порнографии</small></span><span class="sw ${r.premod?'on':''}"></span></button></div>
    <div class="card"><div class="pad"><span class="lbl">Запрещённые слова</span><span class="note">Одна строка — одно слово или фраза. Срабатывает по началу слова: «порн» ловит «порно» и «порнуха», но не «топорно». Строки с # — комментарии. Посты, комментарии, имена и ники с этими словами не публикуются.</span>
    <textarea class="field" id="asW" rows="14" spellcheck="false" style="font-family:ui-monospace,Menlo,Consolas,monospace;font-size:.88em"></textarea><button class="btn" id="asSave">Сохранить слова</button></div></div>
    <div class="card"><div class="pad"><span class="lbl">Как работает модерация</span><span class="note">Картинки в личных сообщениях зашифрованы, их не видит никто, кроме собеседников, в том числе вы. Нарушителя в личке можно заблокировать по жалобе. В ленте пост скрывается автоматически после ${3} жалоб от разных людей.</span></div></div>`;
  const w=box.querySelector('#asW');w.value=r.words;let pre=r.premod;
  box.querySelector('#asPre').onclick=async()=>{try{const x=await api('admin/settings',{premod:!pre});pre=x.premod;box.querySelector('#asPre .sw').classList.toggle('on',pre);toast(pre?'Картинки проверяются перед публикацией':'Проверка картинок выключена')}catch(e){toast(e.message)}};
  box.querySelector('#asSave').onclick=async()=>{try{await api('admin/settings',{premod:pre,words:w.value});toast('Список слов сохранён')}catch(e){toast(e.message)}};
}

/* ---------- обвязка ---------- */
function wire(){
  $('#bMenu').innerHTML=I.menu;$('#fab').innerHTML=I.pen;$('#cBack').innerHTML=I.back;$('#cMore').innerHTML=I.dots;$('#bAttach').innerHTML=I.photo;$('#bSend').innerHTML=I.send;$('#toBottom').innerHTML=I.down;$('#bSaved').innerHTML=I.bookmark;
  $('#bMenu').onclick=profilePage;$('#fab').onclick=()=>tab==='feed'?newPostPage():findPage();
  $('#tabs').onclick=e=>{const b=e.target.closest('[data-tab]');if(b)setTab(b.dataset.tab)};
  wireFeed();$('#bSaved').onclick=()=>openChat(me.id);
  $('#q').oninput=renderList;
  $('#list').onclick=e=>{const a=e.target.closest('[data-act]');if(a){a.dataset.act==='find'?findPage():copy(me.id,'ID скопирован');return}const r=e.target.closest('[data-peer]');if(r)openChat(r.dataset.peer)};
  $('#cBack').onclick=()=>back();$('#cMore').onclick=chatInfo;$('#cTtl').onclick=chatInfo;$('#cAv').onclick=chatInfo;
  const box=$('#msgs');
  box.addEventListener('scroll',()=>{$('#toBottom').hidden=box.scrollHeight-box.scrollTop-box.clientHeight<300},{passive:true});
  box.addEventListener('click',async e=>{
    const j=e.target.closest('[data-jump]');if(j){const el=box.querySelector(`[data-id="${CSS.escape(j.dataset.jump)}"]`);if(el){el.scrollIntoView({block:'center',behavior:'smooth'});el.classList.remove('flash');void el.offsetWidth;el.classList.add('flash')}return}
    if(e.target.closest('[data-more]')){const c=chats.get(cur);box._older=true;await loadMsgs(c,true);renderChat();return}
    const im=e.target.closest('[data-view]');if(im){viewImg(im.src);return}
    if(e.target.closest('a'))return;const m=e.target.closest('.msg');if(m)msgMenu(m.dataset.id);
  });
  $('#toBottom').onclick=()=>box.scrollTo({top:box.scrollHeight,behavior:'smooth'});
  const inp=$('#inp');inp.oninput=()=>{autosize();if(inp.value.trim())typing()};
  inp.onkeydown=e=>{if(e.key==='Enter'&&!e.shiftKey&&!coarse){e.preventDefault();doSend()}};
  $('#bSend').onclick=doSend;$('#bAttach').onclick=()=>$('#fImg').click();
  document.addEventListener('visibilitychange',()=>{if(!document.hidden&&cur)markRead(chats.get(cur))});
  addEventListener('hashchange',()=>{if(me&&myPriv)handleHash()});
  if(window.visualViewport){const vv=visualViewport;const fit=()=>{const a=$('#app');a.style.height=vv.height+'px';a.style.top=vv.offsetTop+'px'};vv.addEventListener('resize',fit);vv.addEventListener('scroll',fit)}
  setInterval(()=>{for(const c of chats.values())if(c.typing&&c.typing<Date.now()){c.typing=0;refresh(c)}},2000);
}
async function boot(){
  applyTheme();wire();
  if(!window.isSecureContext||!crypto.subtle){$('#bootMsg').innerHTML='Откройте страницу по адресу, который начинается с <b>https://</b>. Без защищённого соединения браузер не даёт доступа к шифрованию.';$('.spin').hidden=true;return}
  try{const k=await SUB.generateKey({name:'X25519'},false,['deriveBits']);if(!k)throw 0}catch(e){$('#bootMsg').textContent='Браузер устарел: нет нужного шифрования. Обновите его до свежей версии (Chrome 133+, Safari 17+, Firefox 130+).';$('.spin').hidden=true;return}
  try{srvInfo=await api('info')}catch(e){}
  resume();
}
boot();
